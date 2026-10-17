package etcdraft

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// Sender leva uma mensagem do Raft, já serializada, ao par `to`. Devolve false quando a
// mensagem foi descartada (fila do par cheia); nunca bloqueia o laço do Ready (SG-10).
type Sender interface {
	Send(to uint64, msg []byte) bool
}

// Config do nó Raft local.
type Config struct {
	ID           uint64
	Voters       []uint64
	Learners     []uint64 // vazio na POC (G-08)
	Dir          string   // diretório do WAL; um subdiretório do volume (C-04)
	TickInterval time.Duration
	// ElectionTicks é o timeout de eleição em ticks (ELECTION_TICKS, ADR-060). O heartbeat
	// é sempre 1 tick. 0 = 10 (1 s com tick de 100 ms).
	ElectionTicks int
	// Applied é o índice até onde o Raft NÃO reentrega as entradas commitadas na partida
	// (ADR-060, item 5): o índice do progresso do applier - 1. É limitado ao commit
	// gravado e nunca fica abaixo do snapshot.
	Applied uint64
	Metrics port.Metrics // eventos do Raft (O-16); nulo = sem métricas
}

// Node é o motor de consenso real. Implementa port.ConsensusEngine, port.RaftInbound e
// port.RaftStatus.
type Node struct {
	cfg      Config
	node     raft.Node
	storage  *raft.MemoryStorage
	wal      *wal.WAL
	send     Sender
	log      zerolog.Logger
	metrics  port.Metrics
	learners map[uint64]bool

	events chan port.LearnerProgress

	// O commit é um SINAL, não uma fila (ADR-060): o laço do Ready publica o índice e
	// fecha commitCh; quem consome lê as entradas por faixa do MemoryStorage. O laço nunca
	// espera o CommitPump, o gate ou o applier.
	commitMu sync.Mutex
	commitCh chan struct{}

	lead    atomic.Uint64
	term    atomic.Uint64
	commit  atomic.Uint64
	running atomic.Bool

	nextProposal atomic.Uint64

	mu      sync.Mutex
	pending map[uint64]chan domain.Index // por ProposalID
	reads   map[string]chan domain.Index // por contexto do ReadIndex
}

var (
	_ port.ConsensusEngine = (*Node)(nil)
	_ port.RaftInbound     = (*Node)(nil)
	_ port.RaftStatus      = (*Node)(nil)
)

// Open abre o WAL (ou cria, na partida do zero) e reinicia o nó Raft a partir dele.
// Votantes ou learners diferentes do que está gravado impedem a partida (RT-10).
func Open(cfg Config, send Sender, log zerolog.Logger) (*Node, error) {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 100 * time.Millisecond
	}
	w, ms, err := openStorage(cfg)
	if err != nil {
		return nil, err
	}
	n := &Node{
		cfg: cfg, storage: ms, wal: w, send: send, log: log, metrics: port.MetricsOrNop(cfg.Metrics),
		learners: make(map[uint64]bool, len(cfg.Learners)),
		events:   make(chan port.LearnerProgress, 1024),
		commitCh: make(chan struct{}),
		pending:  make(map[uint64]chan domain.Index),
		reads:    make(map[string]chan domain.Index),
	}
	for _, l := range cfg.Learners {
		n.learners[l] = true
	}
	n.nextProposal.Store(uint64(time.Now().UnixNano()))
	if hs, _, err := ms.InitialState(); err == nil {
		n.term.Store(hs.GetTerm())
		n.commit.Store(hs.GetCommit())
	}
	snap, err := ms.Snapshot()
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if cfg.ElectionTicks <= 0 {
		cfg.ElectionTicks = 10
		n.cfg.ElectionTicks = 10
	}
	n.node = raft.RestartNode(&raft.Config{
		ID:                        cfg.ID,
		ElectionTick:              cfg.ElectionTicks,
		HeartbeatTick:             1,
		Storage:                   ms,
		Applied:                   restartApplied(snap.GetMetadata().GetIndex(), n.commit.Load(), cfg.Applied),
		MaxSizePerMsg:             1 << 20,
		MaxInflightMsgs:           256,
		MaxUncommittedEntriesSize: 32 << 20,
		CheckQuorum:               true,
		PreVote:                   true,
		DisableProposalForwarding: true, // o encaminhamento é do DR (ADR-010)
		Logger:                    raftLogger{log},
	})
	return n, nil
}

// restartApplied limita o Applied da partida: nunca abaixo do snapshot nem acima do commit
// gravado (o Raft entra em pânico com Applied > commit).
func restartApplied(snap, commit, want uint64) uint64 {
	return max(snap, min(want, commit))
}

// Run é o laço do Ready no modo síncrono com F3 (RT-04, ADR-056). Devolve erro só em
// falha fatal (fsync, snapshot recebido, entrada ilegível); o chamador derruba o processo.
func (n *Node) Run(ctx context.Context) error {
	n.running.Store(true)
	defer n.running.Store(false)
	tick := time.NewTicker(n.cfg.TickInterval)
	defer tick.Stop()
	lastTick := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-tick.C:
			// Intervalo real entre ticks (O-27): bem acima do TickInterval quer dizer laço
			// preso (fsync lento), e cada tick perdido atrasa heartbeat e eleição.
			n.metrics.RaftTickGap(now.Sub(lastTick))
			lastTick = now
			n.node.Tick()
		case rd := <-n.node.Ready():
			if err := n.handleReady(ctx, rd); err != nil {
				return err
			}
			n.node.Advance()
		}
	}
}

func (n *Node) handleReady(ctx context.Context, rd raft.Ready) error {
	start := time.Now()
	if rd.SoftState != nil {
		if old := n.lead.Swap(rd.SoftState.Lead); old != rd.SoftState.Lead {
			n.log.Info().Uint64("lider", rd.SoftState.Lead).Uint64("termo", n.term.Load()).Msg("mudança de líder")
			n.metrics.LeaderChanged(rd.SoftState.Lead)
		}
	}
	if !raft.IsEmptySnap(rd.Snapshot) {
		return errors.New("etcdraft: snapshot recebido de um par; a POC não usa snapshots (RT-11)")
	}
	isLeader := n.lead.Load() == n.cfg.ID
	before, after := domain.SplitBeforeSync(isLeader, rd.Messages, kindOf)
	n.sendAll(before) // (1) só o MsgApp da líder sai antes do fsync
	persist := len(rd.Entries) > 0 || !raft.IsEmptyHardState(rd.HardState)
	t0 := time.Now()
	if err := n.wal.Save(rd.HardState, rd.Entries); err != nil { // (2) group commit com fdatasync
		return fmt.Errorf("etcdraft: wal.Save: %w", err) // fatal, nunca repetir (RT-05)
	}
	if persist {
		n.metrics.WALFsync(time.Since(t0)) // só incremento atômico: não atrasa o laço
	}
	if err := n.storage.Append(rd.Entries); err != nil { // (3)
		return fmt.Errorf("etcdraft: MemoryStorage.Append: %w", err)
	}
	if !raft.IsEmptyHardState(rd.HardState) {
		_ = n.storage.SetHardState(rd.HardState)
		n.term.Store(rd.HardState.GetTerm())
	}
	n.sendAll(after) // (4) MsgAppResp, votos e o resto, só depois do fsync (SG-03)
	for _, rs := range rd.ReadStates {
		n.mu.Lock()
		ch := n.reads[string(rs.RequestCtx)]
		n.mu.Unlock()
		if ch != nil {
			select {
			case ch <- domain.Index(rs.Index):
			default:
			}
		}
	}
	if len(rd.CommittedEntries) > 0 { // (5) publica o commit: sinal, nunca fila (ADR-060)
		if err := n.notifyProposals(rd.CommittedEntries); err != nil {
			return err
		}
		n.publishCommit(rd.CommittedEntries[len(rd.CommittedEntries)-1].GetIndex())
	}
	n.metrics.RaftLoop(time.Since(start)) // fsync, envio e publicação: o Ready inteiro
	return nil                            // (6) Advance é chamado por Run, sem esperar ninguém
}

// publishCommit avança o commit publicado e acorda quem espera. As entradas até ele já
// estão no MemoryStorage (passo 3), então quem lê por faixa sempre as encontra.
func (n *Node) publishCommit(idx uint64) {
	n.commitMu.Lock()
	defer n.commitMu.Unlock()
	if idx <= n.commit.Load() {
		return
	}
	n.commit.Store(idx)
	close(n.commitCh)
	n.commitCh = make(chan struct{})
}

func (n *Node) sendAll(msgs []*raftpb.Message) {
	for _, m := range msgs {
		if m.GetType() == raftpb.MsgSnap {
			n.log.Error().Uint64("para", m.GetTo()).Msg("MsgSnap não enviado: a POC não usa snapshots (RT-11)")
			n.node.ReportSnapshot(m.GetTo(), raft.SnapshotFailure)
			continue
		}
		data, err := proto.Marshal(m)
		if err != nil {
			n.log.Error().Err(err).Msg("mensagem do Raft não serializável")
			continue
		}
		n.metrics.RaftMessageSent(typeName(m))
		if !n.send.Send(m.GetTo(), data) {
			n.node.ReportUnreachable(m.GetTo())
		}
	}
}

// notifyProposals acorda os Propose desta célula pelo ProposalID. Só decodifica quando há
// alguém esperando (só na líder): a follower não paga o JSON dentro do laço.
func (n *Node) notifyProposals(ents []*raftpb.Entry) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.pending) == 0 {
		return nil
	}
	for _, e := range ents {
		de, err := toDomain(e)
		if err != nil {
			return err
		}
		if de.ProposalID == 0 {
			continue
		}
		if ch := n.pending[de.ProposalID]; ch != nil {
			select {
			case ch <- de.Index:
			default:
			}
		}
	}
	return nil
}

// Propose propõe a entrada e devolve o índice quando ela é commitada, casando pelo
// ProposalID (RT-07). ErrProposalDropped vira ErrBackpressure (nada entrou no log);
// prazo estourado vira ErrPending (resultado desconhecido).
func (n *Node) Propose(ctx context.Context, e domain.Entry) (domain.Index, error) {
	e.ProposalID = n.nextProposal.Add(1)
	ch := make(chan domain.Index, 1)
	n.mu.Lock()
	n.pending[e.ProposalID] = ch
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		delete(n.pending, e.ProposalID)
		n.mu.Unlock()
	}()
	data, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	if err := n.node.Propose(ctx, data); err != nil {
		if errors.Is(err, raft.ErrProposalDropped) {
			cause := n.dropCause()
			n.metrics.Throttled(cause)
			return 0, &usecase.BackpressureError{Cause: cause}
		}
		return 0, usecase.ErrPending
	}
	select {
	case idx := <-ch:
		return idx, nil
	case <-ctx.Done():
		return 0, usecase.ErrPending
	}
}

// dropCause diz por que o Raft descartou a proposta (O-27): a célula não é mais a líder
// (n.lead ainda não viu a troca), há transferência de liderança em curso, ou o buffer de
// entradas não commitadas (MaxUncommittedEntriesSize) está cheio.
func (n *Node) dropCause() string {
	st := n.node.Status()
	switch {
	case st.RaftState != raft.StateLeader:
		return usecase.CauseNotLeader
	case st.LeadTransferee != raft.None:
		return usecase.CauseTransfer
	default:
		return usecase.CauseBufferFull
	}
}

// ReadIndex confirma a liderança com o quórum e devolve o índice de leitura. Só na líder.
func (n *Node) ReadIndex(ctx context.Context) (domain.Index, error) {
	if n.lead.Load() != n.cfg.ID {
		return 0, usecase.ErrNotLeader
	}
	rctx := make([]byte, 16)
	_, _ = rand.Read(rctx)
	ch := make(chan domain.Index, 1)
	n.mu.Lock()
	n.reads[string(rctx)] = ch
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		delete(n.reads, string(rctx))
		n.mu.Unlock()
	}()
	if err := n.node.ReadIndex(ctx, rctx); err != nil {
		return 0, usecase.ErrReadTimeout
	}
	select {
	case idx := <-ch:
		return idx, nil
	case <-ctx.Done():
		return 0, usecase.ErrReadTimeout
	}
}

// Step entrega ao Raft as mensagens recebidas de um par. Uma resposta de AppendEntries
// sem rejeição, vinda de um learner, vira evento de progresso (G-04): heartbeat não conta
// e nenhum evento é descartado (o canal bloqueia).
func (n *Node) Step(ctx context.Context, msgs [][]byte) error {
	for _, data := range msgs {
		m := &raftpb.Message{}
		if err := proto.Unmarshal(data, m); err != nil {
			return fmt.Errorf("etcdraft: mensagem ilegível: %w", err)
		}
		n.metrics.RaftMessageReceived(typeName(m))
		if t := m.GetType(); t == raftpb.MsgHeartbeat || t == raftpb.MsgHeartbeatResp {
			n.metrics.HeartbeatSeen()
		}
		if m.GetType() == raftpb.MsgAppResp && !m.GetReject() && n.learners[m.GetFrom()] {
			ev := port.LearnerProgress{
				Term: domain.Term(m.GetTerm()), Index: domain.Index(m.GetIndex()),
				Learner: strconv.FormatUint(m.GetFrom(), 10), SentAt: time.Now(),
			}
			select {
			case n.events <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := n.node.Step(ctx, m); err != nil && !errors.Is(err, raft.ErrStopped) {
			return err
		}
	}
	return nil
}

func (n *Node) Leader() (port.LeaderInfo, bool) {
	l := n.lead.Load()
	if l == raft.None {
		return port.LeaderInfo{}, false
	}
	return port.LeaderInfo{NodeID: strconv.FormatUint(l, 10), Local: l == n.cfg.ID}, true
}

func (n *Node) TransferLeadership(ctx context.Context, to string) error {
	id, err := strconv.ParseUint(to, 10, 64)
	if err != nil {
		return fmt.Errorf("etcdraft: nó inválido %q", to)
	}
	n.node.TransferLeadership(ctx, n.lead.Load(), id)
	return nil
}

// CommitIndex é o último índice commitado publicado pelo laço (ADR-060).
func (n *Node) CommitIndex() domain.Index { return domain.Index(n.commit.Load()) }

// CommitChanged devolve um canal fechado no próximo avanço do commit. Pegue o canal ANTES
// de ler CommitIndex, para não perder um avanço.
func (n *Node) CommitChanged() <-chan struct{} {
	n.commitMu.Lock()
	defer n.commitMu.Unlock()
	return n.commitCh
}

// FirstIndex é o primeiro índice ainda no MemoryStorage (o snapshot de partida fica antes).
func (n *Node) FirstIndex() domain.Index {
	fi, err := n.storage.FirstIndex()
	if err != nil {
		return 1
	}
	return domain.Index(fi)
}

// CommittedEntries lê do MemoryStorage as entradas commitadas de lo a hi, inclusive, até
// maxBytes (pelo menos uma). Roda fora do laço do Ready: o MemoryStorage tem trava própria.
func (n *Node) CommittedEntries(lo, hi domain.Index, maxBytes uint64) ([]domain.Entry, error) {
	if c := n.CommitIndex(); hi > c {
		hi = c
	}
	if lo > hi {
		return nil, nil
	}
	ents, err := n.storage.Entries(uint64(lo), uint64(hi)+1, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("etcdraft: ler entradas [%d, %d]: %w", lo, hi, err)
	}
	out := make([]domain.Entry, 0, len(ents))
	for _, e := range ents {
		de, err := toDomain(e)
		if err != nil {
			return nil, err
		}
		out = append(out, de)
	}
	return out, nil
}

func (n *Node) Term() domain.Term                          { return domain.Term(n.term.Load()) }
func (n *Node) LearnerEvents() <-chan port.LearnerProgress { return n.events }

// LearnerDurable não tem retrato na POC: o progresso dos learners chega só por evento.
func (n *Node) LearnerDurable() []port.LearnerProgress { return nil }

// Status devolve o retrato do Raft local; Match só na líder.
func (n *Node) Status() port.RaftInfo {
	st := n.node.Status()
	info := port.RaftInfo{
		NodeID: n.cfg.ID, Lead: st.Lead, Term: domain.Term(st.HardState.GetTerm()),
		Commit: domain.Index(st.HardState.GetCommit()), Running: n.running.Load(),
	}
	info.Voters = len(n.cfg.Voters)
	if len(st.Progress) > 0 {
		for _, id := range n.cfg.Voters { // a líder conta a si mesma como ativa
			if pr, ok := st.Progress[id]; ok && (pr.RecentActive || id == n.cfg.ID) {
				info.QuorumActive++
			}
		}
		info.Match = make(map[uint64]domain.Index, len(st.Progress))
		ids := make([]uint64, 0, len(st.Progress))
		for id := range st.Progress {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			info.Match[id] = domain.Index(st.Progress[id].Match)
		}
	}
	return info
}

// Close para o nó e fecha o WAL.
func (n *Node) Close() error {
	n.node.Stop()
	return n.wal.Close()
}

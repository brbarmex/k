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
}

// Node é o motor de consenso real. Implementa port.ConsensusEngine, port.RaftCompactor,
// port.RaftInbound e port.RaftStatus.
type Node struct {
	cfg      Config
	node     raft.Node
	storage  *raft.MemoryStorage
	wal      *wal.WAL
	send     Sender
	log      zerolog.Logger
	learners map[uint64]bool

	committed chan port.CommitBatch
	events    chan port.LearnerProgress

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
	_ port.RaftCompactor   = (*Node)(nil)
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
		cfg: cfg, storage: ms, wal: w, send: send, log: log,
		learners:  make(map[uint64]bool, len(cfg.Learners)),
		committed: make(chan port.CommitBatch, 1024),
		events:    make(chan port.LearnerProgress, 1024),
		pending:   make(map[uint64]chan domain.Index),
		reads:     make(map[string]chan domain.Index),
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
	n.node = raft.RestartNode(&raft.Config{
		ID:                        cfg.ID,
		ElectionTick:              10,
		HeartbeatTick:             1,
		Storage:                   ms,
		Applied:                   snap.GetMetadata().GetIndex(), // RT-09: o applier pula pelo progresso salvo
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

// Run é o laço do Ready no modo síncrono com F3 (RT-04, ADR-056). Devolve erro só em
// falha fatal (fsync, snapshot recebido, entrada ilegível); o chamador derruba o processo.
func (n *Node) Run(ctx context.Context) error {
	n.running.Store(true)
	defer n.running.Store(false)
	tick := time.NewTicker(n.cfg.TickInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
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
	if rd.SoftState != nil {
		if old := n.lead.Swap(rd.SoftState.Lead); old != rd.SoftState.Lead {
			n.log.Info().Uint64("lider", rd.SoftState.Lead).Uint64("termo", n.term.Load()).Msg("mudança de líder")
		}
	}
	if !raft.IsEmptySnap(rd.Snapshot) {
		return errors.New("etcdraft: snapshot recebido de um par; a POC não usa snapshots (RT-11)")
	}
	isLeader := n.lead.Load() == n.cfg.ID
	before, after := domain.SplitBeforeSync(isLeader, rd.Messages, kindOf)
	n.sendAll(before)                                            // (1) só o MsgApp da líder sai antes do fsync
	if err := n.wal.Save(rd.HardState, rd.Entries); err != nil { // (2) group commit com fdatasync
		return fmt.Errorf("etcdraft: wal.Save: %w", err) // fatal, nunca repetir (RT-05)
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
	if len(rd.CommittedEntries) > 0 { // (5) ao CommitPump
		batch := port.CommitBatch{Entries: make([]domain.Entry, 0, len(rd.CommittedEntries))}
		for _, e := range rd.CommittedEntries {
			de, err := toDomain(e)
			if err != nil {
				return err
			}
			batch.Entries = append(batch.Entries, de)
		}
		n.commit.Store(rd.CommittedEntries[len(rd.CommittedEntries)-1].GetIndex())
		n.notifyProposals(batch.Entries)
		select {
		case n.committed <- batch: // bloqueante: applier lento segura o laço (risco 3)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil // (6) Advance é chamado por Run
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
		if !n.send.Send(m.GetTo(), data) {
			n.node.ReportUnreachable(m.GetTo())
		}
	}
}

func (n *Node) notifyProposals(entries []domain.Entry) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range entries {
		if e.ProposalID == 0 {
			continue
		}
		if ch := n.pending[e.ProposalID]; ch != nil {
			select {
			case ch <- e.Index:
			default:
			}
		}
	}
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
			return 0, usecase.ErrBackpressure
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

func (n *Node) Committed() <-chan port.CommitBatch         { return n.committed }
func (n *Node) Term() domain.Term                          { return domain.Term(n.term.Load()) }
func (n *Node) LearnerEvents() <-chan port.LearnerProgress { return n.events }

// LearnerDurable não tem retrato na POC: o progresso dos learners chega só por evento.
func (n *Node) LearnerDurable() []port.LearnerProgress { return nil }

// MinPeerMatch e Compact: a compactação do Raft fica desligada na POC (RT-11, AP-10).
func (n *Node) MinPeerMatch() domain.Index { return 0 }

func (n *Node) Compact(upTo domain.Index) error {
	n.log.Debug().Uint64("ate", uint64(upTo)).Msg("compactação do Raft desligada na POC")
	return nil
}

// Status devolve o retrato do Raft local; Match só na líder.
func (n *Node) Status() port.RaftInfo {
	st := n.node.Status()
	info := port.RaftInfo{
		NodeID: n.cfg.ID, Lead: st.Lead, Term: domain.Term(st.HardState.GetTerm()),
		Commit: domain.Index(st.HardState.GetCommit()), Running: n.running.Load(),
	}
	if len(st.Progress) > 0 {
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

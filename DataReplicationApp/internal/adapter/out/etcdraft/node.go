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
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
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
	Dir          string // diretório do WAL; um subdiretório do volume (C-04)
	TickInterval time.Duration
	// ElectionTicks é o timeout de eleição em ticks (ELECTION_TICKS, ADR-060). O heartbeat
	// é sempre 1 tick. 0 = 10 (1 s com tick de 100 ms).
	ElectionTicks int
	// Applied é o índice até onde o Raft NÃO reentrega as entradas commitadas na partida
	// (ADR-060, item 5): o índice do progresso do applier - 1. É limitado ao commit
	// gravado e nunca fica abaixo do snapshot.
	Applied uint64
	Metrics port.Metrics // eventos do Raft (O-16); nulo = sem métricas

	// ClusterID é a identidade do grupo (RT-20), gravada no metadado do WAL e no marcador.
	ClusterID string
	// SegmentBytes é o tamanho do segmento do WAL (WAL_SEGMENT_BYTES, DC-07); 0 não altera.
	SegmentBytes int64
	// MarkerPath é o arquivo do marcador (DC-06), ao lado do progress; vazio = sem arquivo.
	MarkerPath string
	// Policy decide se um MsgSnap recebido chega ao Raft (RT-18, SG-19); nula = aceita
	// todos (só testes).
	Policy port.SnapshotPolicy
	// Genesis são os tokens de partida vistos no nascimento (DC-12, Incremento B).
	Genesis map[uint64]string
	// Compaction liga o Compactor (RT-11, RT-14, RT-15); Progress nulo = desligado.
	Compaction CompactionConfig
	// ReplClock recebe as amostras da líder (DC-16): o último índice de cada Append e o
	// commit publicado. Nulo = desligado.
	ReplClock *domain.ReplicationClock
}

// Node é o motor de consenso real. Implementa port.ConsensusEngine, port.RaftInbound e
// port.RaftStatus.
type Node struct {
	cfg     Config
	node    raft.Node
	storage *raft.MemoryStorage
	wal     *wal.WAL
	send    Sender
	log     zerolog.Logger
	metrics port.Metrics

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

	// Log limitado (RT-09, RT-11, RT-14 a RT-18).
	ram       ramTracker
	advanced  atomic.Uint64 // último índice entregue pelo laço E avançado (DC-05)
	deferred  atomic.Uint64 // maior índice de marcador descartado por aplicado atrás (RT-18)
	holeIdx   atomic.Uint64 // índice do último marcador recebido: o log local não tem nada até ele (DC-18)
	snapMu    sync.Mutex
	snapIdx   domain.Index          // índice do último snapshot (WAL e MemoryStorage)
	marker    domain.SnapshotMarker // marcador do último snapshot
	cs        *raftpb.ConfState     // membros (a POC não troca membros)
	genesis   map[uint64]string
	ret       *Retention
	reader    *LogReader
	compactor *Compactor
	snapLog   *rateLog
}

var (
	_ port.ConsensusEngine = (*Node)(nil)
	_ port.RaftInbound     = (*Node)(nil)
	_ port.RaftStatus      = (*Node)(nil)
)

// Open abre o WAL (ou cria, na partida do zero) e reinicia o nó Raft a partir do último
// snapshot: só a cauda volta para a RAM (RT-09). Votantes diferentes do que está gravado
// (RT-10) ou outra identidade (RT-20) impedem a partida.
func Open(cfg Config, send Sender, log zerolog.Logger) (*Node, error) {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 100 * time.Millisecond
	}
	t0 := time.Now()
	o, err := openStorage(cfg)
	if err != nil {
		return nil, err
	}
	w, ms := o.w, o.ms
	n := &Node{
		cfg: cfg, storage: ms, wal: w, send: send, log: log, metrics: port.MetricsOrNop(cfg.Metrics),
		commitCh: make(chan struct{}),
		pending:  make(map[uint64]chan domain.Index),
		reads:    make(map[string]chan domain.Index),
		snapIdx:  o.snap, marker: o.marker, cs: o.cs, genesis: o.genesis,
		snapLog: newRateLog(10 * time.Second),
	}
	hole, err := readHole(holePath(cfg.MarkerPath))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	n.holeIdx.Store(uint64(hole))
	n.ram.recordSnapshot(o.snap)
	n.ram.recordAppend(o.ents)
	n.advanced.Store(uint64(o.snap))
	n.ret = newRetention(cfg.Dir)
	n.reader = &LogReader{node: n, ret: n.ret, dir: cfg.Dir}
	if cfg.Compaction.Progress != nil {
		n.compactor = newCompactor(n, cfg.Compaction, n.ret)
	}
	ev := log.Info().Uint64("snapshot", uint64(o.snap)).Int("entradas", len(o.ents))
	if len(o.ents) > 0 {
		ev = ev.Uint64("primeiro", o.ents[0].GetIndex()).Uint64("ultimo", o.ents[len(o.ents)-1].GetIndex())
	}
	ev.Dur("duracao", time.Since(t0)).Msg("partida: WAL relido")
	n.metrics.StartupDuration(time.Since(t0))
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
// falha fatal (fsync, marcador recebido ilegível, entrada ilegível); o chamador derruba
// o processo.
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
			// DC-05: só depois do Advance o etcd-raft considera as entradas aplicadas; é o
			// limite seguro do snapshot e da compactação da RAM.
			if len(rd.CommittedEntries) > 0 {
				n.storeAdvanced(rd.CommittedEntries[len(rd.CommittedEntries)-1].GetIndex())
			}
			if !raft.IsEmptySnap(rd.Snapshot) {
				n.storeAdvanced(rd.Snapshot.GetMetadata().GetIndex())
			}
		}
	}
}

func (n *Node) handleReady(ctx context.Context, rd raft.Ready) error {
	start := time.Now()
	if rd.SoftState != nil {
		if old := n.lead.Swap(rd.SoftState.Lead); old != rd.SoftState.Lead {
			n.log.Info().Uint64("lider", rd.SoftState.Lead).Uint64("termo", n.term.Load()).Msg("mudança de líder")
			n.metrics.LeaderChanged(rd.SoftState.Lead)
			if rd.SoftState.Lead == n.cfg.ID && n.cfg.ReplClock != nil {
				n.cfg.ReplClock.Reset(start) // nova liderança: mede só o que ela propõe (DC-16)
			}
		}
	}
	// RT-18: marcador aceito pelo Step (aplicado ≥ S). É a exceção da SG-22: o recebimento
	// grava no laço, como faz o etcd, porque é um registro pequeno e o Raft espera por ele.
	snapIn := !raft.IsEmptySnap(rd.Snapshot)
	var snapMarker domain.SnapshotMarker
	if snapIn {
		var err error
		if snapMarker, err = n.saveReceivedSnapshot(rd.Snapshot); err != nil {
			return err
		}
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
	if snapIn { // o snapshot entra no MemoryStorage antes das entradas que vêm depois dele
		if err := n.storage.ApplySnapshot(rd.Snapshot); err != nil {
			return fmt.Errorf("etcdraft: MemoryStorage.ApplySnapshot: %w", err)
		}
		s := domain.Index(rd.Snapshot.GetMetadata().GetIndex())
		n.ram.recordSnapshot(s)
		n.setSnapshot(s, snapMarker)
	}
	if err := n.storage.Append(rd.Entries); err != nil { // (3)
		return fmt.Errorf("etcdraft: MemoryStorage.Append: %w", err)
	}
	n.ram.recordAppend(rd.Entries)
	if isLeader && len(rd.Entries) > 0 && n.cfg.ReplClock != nil {
		n.cfg.ReplClock.RecordAppend(domain.Index(rd.Entries[len(rd.Entries)-1].GetIndex()), start)
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
	if snapIn {
		n.publishCommit(rd.Snapshot.GetMetadata().GetIndex())
	}
	if len(rd.CommittedEntries) > 0 { // (5) publica o commit: sinal, nunca fila (ADR-060)
		if err := n.notifyProposals(rd.CommittedEntries); err != nil {
			return err
		}
		last := rd.CommittedEntries[len(rd.CommittedEntries)-1].GetIndex()
		n.publishCommit(last)
		if isLeader && n.cfg.ReplClock != nil {
			// O instante é o início do Ready: o Raft já tinha decidido o commit; a diferença
			// para a publicação é no máximo um Ready (DC-16).
			n.cfg.ReplClock.RecordCommit(domain.Index(last), start)
		}
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

// saveReceivedSnapshot grava um marcador aceito (RT-18), na ordem: arquivo do marcador e
// wal.SaveSnapshot, antes do wal.Save do mesmo Ready. Qualquer erro é fatal.
func (n *Node) saveReceivedSnapshot(snap *raftpb.Snapshot) (domain.SnapshotMarker, error) {
	m, err := domain.DecodeMarker(snap.GetData())
	if err != nil {
		return m, fmt.Errorf("etcdraft: marcador recebido ilegível: %w", err)
	}
	md := snap.GetMetadata()
	s := domain.Index(md.GetIndex())
	// DC-18: o buraco é gravado antes do marcador e do WAL; uma queda entre os passos só
	// deixa o buraco maior que o necessário, o que é seguro.
	if err := writeHole(holePath(n.cfg.MarkerPath), s); err != nil {
		return m, err
	}
	n.holeIdx.Store(uint64(s))
	if err := writeMarker(n.cfg.MarkerPath, m, s); err != nil {
		return m, err
	}
	if err := n.wal.SaveSnapshot(&walpb.Snapshot{Index: proto.Uint64(md.GetIndex()), Term: proto.Uint64(md.GetTerm()), ConfState: md.GetConfState()}); err != nil {
		return m, fmt.Errorf("etcdraft: wal.SaveSnapshot do marcador recebido: %w", err)
	}
	n.log.Info().Uint64("indice", uint64(s)).Uint64("confirmado", uint64(m.Confirmed)).Msg("marcador recebido e gravado")
	return m, nil
}

// sendAll envia as mensagens do Raft. O MsgSnap leva só o marcador (RT-17): sai pelo
// transporte normal e o resultado do enfileiramento vira o relato ao Raft. O transporte é
// assíncrono; um relato otimista só provoca uma nova sondagem.
func (n *Node) sendAll(msgs []*raftpb.Message) {
	for _, m := range msgs {
		data, err := proto.Marshal(m)
		if err != nil {
			n.log.Error().Err(err).Msg("mensagem do Raft não serializável")
			continue
		}
		n.metrics.RaftMessageSent(typeName(m))
		ok := n.send.Send(m.GetTo(), data)
		if !ok {
			n.node.ReportUnreachable(m.GetTo())
		}
		if m.GetType() != raftpb.MsgSnap {
			continue
		}
		if ok {
			n.node.ReportSnapshot(m.GetTo(), raft.SnapshotFinish)
		} else {
			n.node.ReportSnapshot(m.GetTo(), raft.SnapshotFailure)
		}
		key := "enviado-" + strconv.FormatUint(m.GetTo(), 10)
		if allow, sup := n.snapLog.allow(key, time.Now()); allow {
			n.log.Info().Uint64("para", m.GetTo()).Uint64("indice", m.GetSnapshot().GetMetadata().GetIndex()).
				Bool("enfileirado", ok).Int("suprimidas", sup).Msg("marcador enviado")
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
// (n.lead ainda não viu a troca) ou o buffer de entradas não commitadas
// (MaxUncommittedEntriesSize) está cheio.
func (n *Node) dropCause() string {
	if n.node.Status().RaftState != raft.StateLeader {
		return usecase.CauseNotLeader
	}
	return usecase.CauseBufferFull
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

// Step entrega ao Raft as mensagens recebidas de um par.
func (n *Node) Step(ctx context.Context, msgs [][]byte) error {
	for _, data := range msgs {
		m := &raftpb.Message{}
		if err := proto.Unmarshal(data, m); err != nil {
			return fmt.Errorf("etcdraft: mensagem ilegível: %w", err)
		}
		if m.GetTo() != n.cfg.ID {
			// DC-19: a conexão de um par pode cair noutra célula (o IP de um container
			// reiniciado é reaproveitado por outro). Uma mensagem para outro nó nunca chega ao
			// Raft: um heartbeat com o commit de outro par derrubaria esta célula.
			if ok, sup := n.snapLog.allow("outro-no", time.Now()); ok {
				n.log.Warn().Uint64("para", m.GetTo()).Uint64("de", m.GetFrom()).Int("suprimidas", sup).
					Msg("mensagem do Raft para outro nó descartada")
			}
			continue
		}
		n.metrics.RaftMessageReceived(typeName(m))
		if t := m.GetType(); t == raftpb.MsgHeartbeat || t == raftpb.MsgHeartbeatResp {
			n.metrics.HeartbeatSeen()
		}
		if m.GetType() == raftpb.MsgSnap && !n.acceptSnapshot(m) {
			continue // SG-19: nada chega ao Raft
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

// CommitIndex é o último índice commitado publicado pelo laço (ADR-060).
func (n *Node) CommitIndex() domain.Index { return domain.Index(n.commit.Load()) }

// CommitChanged devolve um canal fechado no próximo avanço do commit. Pegue o canal ANTES
// de ler CommitIndex, para não perder um avanço.
func (n *Node) CommitChanged() <-chan struct{} {
	n.commitMu.Lock()
	defer n.commitMu.Unlock()
	return n.commitCh
}

// FirstIndex é o primeiro índice ainda retido em disco (O-34). Tudo o que está antes dele
// já foi aplicado com o progresso durável (G1).
func (n *Node) FirstIndex() domain.Index { return n.reader.FirstRetained() }

// ramFirst é o primeiro índice ainda no MemoryStorage.
func (n *Node) ramFirst() domain.Index {
	fi, err := n.storage.FirstIndex()
	if err != nil {
		return 1
	}
	return domain.Index(fi)
}

// CommittedEntries lê as entradas commitadas de lo a hi, inclusive, até maxBytes (pelo
// menos uma), pelo LogReader: da RAM ou, abaixo dela, do disco (AP-01, RT-16). Roda fora
// do laço do Ready.
func (n *Node) CommittedEntries(lo, hi domain.Index, maxBytes uint64) ([]domain.Entry, error) {
	return n.reader.Read(context.Background(), lo, hi, maxBytes)
}

func (n *Node) Term() domain.Term { return domain.Term(n.term.Load()) }

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
	info.Snapshot = n.SnapshotIndex()
	info.MemEntries, info.MemBytes = n.ram.stats()
	info.MemMax, info.MemFirst = n.cfg.Compaction.RAMMax, n.ramFirst()
	if d := n.deferred.Load(); d > 0 && (n.cfg.Policy == nil || uint64(n.cfg.Policy.Applied()) < d) {
		info.BehindTail = true
	}
	if n.compactor != nil {
		cs := n.compactor.Status()
		info.RetainedBytes, info.RetainedFiles, info.Floor, info.Pins = cs.RetainedBytes, cs.RetainedFiles, cs.Floor, cs.Pins
		info.HardCeiling, info.HeldByApplied = cs.HardCeiling, cs.HeldByApplied
		if info.Lead == n.cfg.ID {
			info.Window = cs.Window
			info.MemByPeer = cs.MemByPeer
		}
	}
	return info
}

// acceptSnapshot aplica a regra de aceitação do MsgSnap (RT-18, SG-19): um marcador de
// outro grupo é descartado; com o aplicado local atrás do marcador, a mensagem é descartada
// e a política é avisada (a célula está atrás da cauda). Só com o aplicado ≥ S ela chega
// ao Raft.
func (n *Node) acceptSnapshot(m *raftpb.Message) bool {
	now := time.Now()
	d, err := domain.DecodeMarker(m.GetSnapshot().GetData())
	if err != nil || d.ClusterID != n.cfg.ClusterID {
		if ok, sup := n.snapLog.allow("outro-grupo", now); ok {
			n.log.Error().Err(err).Str("grupo", d.ClusterID).Uint64("de", m.GetFrom()).Int("suprimidas", sup).
				Msg("marcador de outro grupo descartado")
		}
		return false
	}
	s := domain.Index(m.GetSnapshot().GetMetadata().GetIndex())
	if p := n.cfg.Policy; p != nil && p.Applied() < s {
		for cur := n.deferred.Load(); uint64(s) > cur && !n.deferred.CompareAndSwap(cur, uint64(s)); cur = n.deferred.Load() {
		}
		n.metrics.SnapshotDeferred()
		if ok, sup := n.snapLog.allow("adiado", now); ok {
			n.log.Warn().Uint64("indice", uint64(s)).Uint64("aplicado", uint64(p.Applied())).Int("suprimidas", sup).
				Msg("marcador adiado: aplicado local atrás do snapshot")
		}
		p.Behind(s)
		return false
	}
	return true
}

// storeAdvanced avança o índice avançado (DC-05), sem recuar.
func (n *Node) storeAdvanced(i uint64) {
	for cur := n.advanced.Load(); i > cur && !n.advanced.CompareAndSwap(cur, i); cur = n.advanced.Load() {
	}
}

// Advanced é o último índice entregue pelo laço e avançado (DC-05): o snapshot e a
// compactação da RAM nunca passam dele.
func (n *Node) Advanced() domain.Index { return domain.Index(n.advanced.Load()) }

// SnapshotIndex é o índice do último snapshot gravado (no WAL e no MemoryStorage).
func (n *Node) SnapshotIndex() domain.Index {
	n.snapMu.Lock()
	defer n.snapMu.Unlock()
	return n.snapIdx
}

// Marker é o marcador do último snapshot: na partida, o confirmado dele restaura o gate (G6).
func (n *Node) Marker() domain.SnapshotMarker {
	n.snapMu.Lock()
	defer n.snapMu.Unlock()
	return n.marker
}

func (n *Node) setSnapshot(s domain.Index, m domain.SnapshotMarker) {
	n.snapMu.Lock()
	defer n.snapMu.Unlock()
	if s > n.snapIdx {
		n.snapIdx, n.marker = s, m
	}
}

func (n *Node) confState() *raftpb.ConfState {
	n.snapMu.Lock()
	defer n.snapMu.Unlock()
	return n.cs
}

// Genesis devolve os tokens de partida gravados no metadado do WAL (DC-12).
func (n *Node) Genesis() map[uint64]string { return n.genesis }

// RAM devolve as entradas e os bytes no MemoryStorage (RT-14).
func (n *Node) RAM() (uint64, int64) { return n.ram.stats() }

// Floor é o piso de retenção do último ciclo (na líder) ou o último recebido (na follower).
func (n *Node) Floor() domain.Index { return n.ret.Floor() }

// LogReader expõe o leitor do log (RT-16).
func (n *Node) LogReader() port.LogSource { return n.reader }

// Pins expõe os pinos de retenção (RT-15); a mesma Retention recebe o piso.
func (n *Node) Pins() *Retention { return n.ret }

// Compactor devolve o compactador, ou nulo quando a compactação está desligada.
func (n *Node) Compactor() *Compactor { return n.compactor }

// hole é o índice do último marcador recebido (DC-18).
func (n *Node) hole() domain.Index { return domain.Index(n.holeIdx.Load()) }

func (n *Node) isLeader() bool { return n.lead.Load() == n.cfg.ID }

// matches é o Match de cada par, visto pela líder.
func (n *Node) matches() map[uint64]domain.Index {
	st := n.node.Status()
	out := make(map[uint64]domain.Index, len(st.Progress))
	for id, pr := range st.Progress {
		out[id] = domain.Index(pr.Match)
	}
	return out
}

// Close para o nó e fecha o WAL.
func (n *Node) Close() error {
	n.node.Stop()
	return n.wal.Close()
}

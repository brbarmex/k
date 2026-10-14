package etcdraft

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// memNet entrega as mensagens entre nós em memória, por goroutine, como o transporte real.
type memNet struct {
	mu    sync.Mutex
	nodes map[uint64]*Node
	down  map[uint64]bool
	sent  []raftpb.MessageType // ordem de envio de quem usa record
	ctx   context.Context
}

type memSender struct {
	net  *memNet
	from uint64
}

func (s memSender) Send(to uint64, msg []byte) bool {
	s.net.mu.Lock()
	dst, down := s.net.nodes[to], s.net.down[to] || s.net.down[s.from]
	s.net.mu.Unlock()
	if dst == nil || down {
		return false
	}
	go func() { _ = dst.Step(s.net.ctx, [][]byte{msg}) }()
	return true
}

type cluster struct {
	t      *testing.T
	net    *memNet
	dirs   map[uint64]string
	cancel map[uint64]context.CancelFunc
	done   map[uint64]chan error
	got    map[uint64]*collector
	m      map[uint64]*recMetrics
}

// collector lê as entradas commitadas por faixa, como o CommitPump faria.
type collector struct {
	mu      sync.Mutex
	entries []domain.Entry
	paused  bool // simula um CommitPump parado (applier ou banco lento)
}

func (c *collector) pause(p bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = p
}

func (c *collector) all() []domain.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]domain.Entry(nil), c.entries...)
}

func newCluster(t *testing.T, ctx context.Context) *cluster {
	base := t.TempDir()
	c := &cluster{
		t:      t,
		net:    &memNet{nodes: map[uint64]*Node{}, down: map[uint64]bool{}, ctx: ctx},
		dirs:   map[uint64]string{},
		cancel: map[uint64]context.CancelFunc{},
		done:   map[uint64]chan error{},
		got:    map[uint64]*collector{},
		m:      map[uint64]*recMetrics{},
	}
	for id := uint64(1); id <= 3; id++ {
		c.dirs[id] = filepath.Join(base, "n"+string(rune('0'+id)), "wal")
		c.start(ctx, id, []uint64{1, 2, 3})
	}
	return c
}

func (c *cluster) start(ctx context.Context, id uint64, voters []uint64) {
	c.t.Helper()
	if c.m[id] == nil {
		c.m[id] = &recMetrics{}
	}
	n, err := Open(Config{ID: id, Voters: voters, Dir: c.dirs[id], TickInterval: 10 * time.Millisecond, Metrics: c.m[id]},
		memSender{net: c.net, from: id}, zerolog.Nop())
	if err != nil {
		c.t.Fatalf("Open(%d): %v", id, err)
	}
	c.net.mu.Lock()
	c.net.nodes[id] = n
	c.net.down[id] = false
	c.net.mu.Unlock()
	nctx, cancel := context.WithCancel(ctx)
	c.cancel[id] = cancel
	done := make(chan error, 1)
	c.done[id] = done
	col := &collector{}
	c.got[id] = col
	go func() { done <- n.Run(nctx) }()
	go func() { // lê por faixa, como o CommitPump (ADR-060)
		next := n.FirstIndex()
		for {
			changed := n.CommitChanged()
			col.mu.Lock()
			paused := col.paused
			col.mu.Unlock()
			if ci := n.CommitIndex(); next <= ci && !paused {
				ents, err := n.CommittedEntries(next, ci, 1<<20)
				if err != nil {
					c.t.Errorf("CommittedEntries(%d, %d): %v", next, ci, err)
					return
				}
				col.mu.Lock()
				col.entries = append(col.entries, ents...)
				col.mu.Unlock()
				next = ents[len(ents)-1].Index + 1
				continue
			}
			select {
			case <-nctx.Done():
				return
			case <-changed:
			case <-time.After(10 * time.Millisecond): // volta a olhar a pausa
			}
		}
	}()
}

func (c *cluster) stop(id uint64) {
	c.net.mu.Lock()
	c.net.down[id] = true
	n := c.net.nodes[id]
	c.net.mu.Unlock()
	c.cancel[id]()
	<-c.done[id]
	_ = n.Close()
}

func (c *cluster) leader(timeout time.Duration) *Node {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.net.mu.Lock()
		var leaders []*Node
		for id, n := range c.net.nodes {
			if c.net.down[id] {
				continue
			}
			if l, ok := n.Leader(); ok && l.Local {
				leaders = append(leaders, n)
			}
		}
		c.net.mu.Unlock()
		if len(leaders) == 1 {
			return leaders[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatal("sem líder única no prazo")
	return nil
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func put(key string) domain.Entry {
	return domain.Entry{Kind: domain.KindWrite, Ops: []domain.Op{{Table: "t_{cell}", Kind: domain.OpPut, Key: []byte(key), Item: []byte(`{}`)}}}
}

// Elege uma líder, commita, entrega índices contíguos (Noop incluso) às três, reinicia
// uma follower e ela recebe de novo as entradas commitadas (RT-03, RT-07, RT-08, RT-09).
func TestClusterElectsCommitsAndRestarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCluster(t, ctx)
	defer func() {
		for id := range c.dirs {
			if !c.net.down[id] {
				c.stop(id)
			}
		}
	}()

	l := c.leader(5 * time.Second)
	// O-16: eleição, heartbeats e fsync aparecem nas métricas.
	lm := c.m[l.cfg.ID]
	waitFor(t, 3*time.Second, func() bool {
		return lm.n("leader") >= 1 && lm.n("sent:heartbeat") > 0 && lm.n("recv:heartbeat_resp") > 0 && lm.n("fsync") > 0
	}, "métricas do Raft não registradas na líder")
	for id, m := range c.m {
		if id != l.cfg.ID {
			waitFor(t, 3*time.Second, func() bool { return m.n("recv:heartbeat") > 0 && m.n("heartbeat") > 0 },
				"a follower devia registrar heartbeats recebidos")
		}
	}
	// O-14: com os três de pé a líder vê os três votantes ativos.
	waitFor(t, 3*time.Second, func() bool { st := l.Status(); return st.QuorumActive == 3 && st.Voters == 3 },
		"a líder devia ver os três votantes ativos")
	pctx, pc := context.WithTimeout(ctx, 3*time.Second)
	defer pc()
	idx, err := l.Propose(pctx, put("a"))
	if err != nil || idx < 2 {
		t.Fatalf("Propose = %d, %v", idx, err)
	}
	idx2, err := l.Propose(pctx, put("b"))
	if err != nil || idx2 <= idx {
		t.Fatalf("segundo Propose = %d, %v", idx2, err)
	}
	for id, col := range c.got {
		waitFor(t, 3*time.Second, func() bool {
			es := col.all()
			return len(es) > 0 && es[len(es)-1].Index >= idx2
		}, "o nó não recebeu as entradas commitadas")
		es := col.all()
		for i, e := range es {
			if e.Index != domain.Index(i+2) {
				t.Fatalf("nó %d: índices não contíguos: %v", id, es)
			}
		}
		if es[0].Kind != domain.KindNoop {
			t.Fatalf("nó %d: a entrada da eleição devia ser Noop, veio %v", id, es[0].Kind)
		}
	}

	var follower uint64
	for id := range c.dirs {
		if id != l.cfg.ID {
			follower = id
			break
		}
	}
	c.stop(follower)
	c.start(ctx, follower, []uint64{1, 2, 3})
	waitFor(t, 5*time.Second, func() bool {
		for _, e := range c.got[follower].all() {
			if e.Index == idx2 && e.Kind == domain.KindWrite && string(e.Ops[0].Key) == "b" {
				return true
			}
		}
		return false
	}, "o nó reiniciado não reentregou as entradas commitadas")
}

// RT-10: votantes diferentes do que está no WAL impedem a partida.
func TestReopenWithOtherMembersFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCluster(t, ctx)
	c.leader(5 * time.Second)
	for id := range c.dirs {
		c.stop(id)
	}
	_, err := Open(Config{ID: 1, Voters: []uint64{1, 2, 3, 4}, Dir: c.dirs[1]}, memSender{net: c.net, from: 1}, zerolog.Nop())
	if !errors.Is(err, ErrMembership) {
		t.Fatalf("erro = %v, quer ErrMembership", err)
	}
}

// G-04: só MsgAppResp sem rejeição de um learner vira evento; heartbeat não conta.
func TestLearnerAppRespBecomesProgressEvent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	n, err := Open(Config{ID: 1, Voters: []uint64{1, 2, 3}, Learners: []uint64{4}, Dir: dir}, memSender{net: &memNet{}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	msg := func(tp raftpb.MessageType, from uint64, reject bool) []byte {
		b, _ := proto.Marshal(&raftpb.Message{Type: tp.Enum(), From: proto.Uint64(from), To: proto.Uint64(1),
			Term: proto.Uint64(1), Index: proto.Uint64(7), Reject: proto.Bool(reject)})
		return b
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = n.Step(ctx, [][]byte{
		msg(raftpb.MsgHeartbeatResp, 4, false),
		msg(raftpb.MsgAppResp, 4, true),
		msg(raftpb.MsgAppResp, 2, false),
		msg(raftpb.MsgAppResp, 4, false),
	})
	select {
	case ev := <-n.LearnerEvents():
		if ev.Learner != "4" || ev.Index != 7 || ev.Term != 1 {
			t.Fatalf("evento = %+v", ev)
		}
	default:
		t.Fatal("o MsgAppResp do learner não virou evento")
	}
	select {
	case ev := <-n.LearnerEvents():
		t.Fatalf("evento a mais: %+v", ev)
	default:
	}
}

// SG-03: no Ready, só o MsgApp da líder sai antes do fsync.
func TestOnlyLeaderMsgAppBeforeSync(t *testing.T) {
	msgs := []*raftpb.Message{
		{Type: raftpb.MsgApp.Enum()}, {Type: raftpb.MsgAppResp.Enum()}, {Type: raftpb.MsgVoteResp.Enum()}, {Type: raftpb.MsgHeartbeat.Enum()},
	}
	before, after := domain.SplitBeforeSync(true, msgs, kindOf)
	if len(before) != 1 || before[0].GetType() != raftpb.MsgApp || len(after) != 3 {
		t.Fatalf("líder: antes=%d depois=%d", len(before), len(after))
	}
	before, _ = domain.SplitBeforeSync(false, msgs, kindOf)
	if len(before) != 0 {
		t.Fatal("follower não envia nada antes do fsync")
	}
}

// Entradas vazias e ConfChange viram Noop; o payload vira a Entry com índice e termo do Raft.
func TestToDomain(t *testing.T) {
	e, err := toDomain(&raftpb.Entry{Index: proto.Uint64(5), Term: proto.Uint64(2), Type: raftpb.EntryConfChange.Enum(), Data: []byte("x")})
	if err != nil || e.Kind != domain.KindNoop || e.Index != 5 {
		t.Fatalf("ConfChange → %+v %v", e, err)
	}
	e, err = toDomain(&raftpb.Entry{Index: proto.Uint64(6), Term: proto.Uint64(2), Type: raftpb.EntryNormal.Enum(), Data: []byte(`{"Kind":1,"ProposalID":9}`)})
	if err != nil || e.Kind != domain.KindWrite || e.Index != 6 || e.Term != 2 || e.ProposalID != 9 {
		t.Fatalf("normal → %+v %v", e, err)
	}
}

// ADR-060: com NINGUÉM consumindo os commits (applier parado em todas as células), o laço
// do Raft segue: as propostas commitam, não há eleição e o commit publicado avança. Antes,
// o canal de commits enchia e o laço parava (teste de carga de 14/10).
func TestRaftLoopNeverWaitsForConsumers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCluster(t, ctx)
	defer func() {
		for id := range c.dirs {
			c.stop(id)
		}
	}()
	l := c.leader(5 * time.Second)
	for _, col := range c.got {
		col.pause(true)
	}
	before := c.m[l.cfg.ID].n("leader")
	const n = 3000     // mais que os 1024 lotes do canal antigo
	const clients = 64 // clientes concorrentes, como o k6; 3000 de uma vez só mede o -race
	var wg sync.WaitGroup
	errs := make(chan error, n)
	next := make(chan int, n)
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	for w := 0; w < clients; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				pctx, pc := context.WithTimeout(ctx, 10*time.Second)
				_, err := l.Propose(pctx, put("k"+strconv.Itoa(i)))
				pc()
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Propose falhou com os consumidores parados: %v", err)
	}
	if got := c.m[l.cfg.ID].n("leader"); got != before {
		t.Fatalf("houve troca de líder com os consumidores parados (%d → %d)", before, got)
	}
	if l.CommitIndex() < n {
		t.Fatalf("commit publicado = %d, quer >= %d", l.CommitIndex(), n)
	}
	for _, col := range c.got { // ao voltar, o consumidor lê tudo da memória do Raft, em ordem
		col.pause(false)
	}
	for id, col := range c.got {
		waitFor(t, 10*time.Second, func() bool {
			es := col.all()
			return len(es) > 0 && es[len(es)-1].Index >= l.CommitIndex()
		}, "o consumidor não alcançou o commit depois de voltar")
		for i, e := range col.all() {
			if e.Index != domain.Index(i+2) {
				t.Fatalf("nó %d: índices não contíguos na posição %d: %d", id, i, e.Index)
			}
		}
	}
}

// O-27: proposta numa follower é descartada pelo Raft com a causa nao_lider.
func TestDropCauseOnFollower(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCluster(t, ctx)
	defer func() {
		for id := range c.dirs {
			c.stop(id)
		}
	}()
	l := c.leader(5 * time.Second)
	var f *Node
	for id, n := range c.net.nodes {
		if id != l.cfg.ID {
			f = n
			break
		}
	}
	waitFor(t, 3*time.Second, func() bool { li, ok := f.Leader(); return ok && !li.Local }, "a follower não viu a líder")
	pctx, pc := context.WithTimeout(ctx, time.Second)
	defer pc()
	_, err := f.Propose(pctx, put("x"))
	var bp *usecase.BackpressureError
	if !errors.As(err, &bp) || bp.Cause != usecase.CauseNotLeader || !errors.Is(err, usecase.ErrBackpressure) {
		t.Fatalf("erro = %v, quer contrapressão nao_lider", err)
	}
	if c.m[f.cfg.ID].n("throttled:nao_lider") != 1 {
		t.Fatal("a causa não foi registrada na métrica")
	}
}

// ADR-060, item 5: o Applied da partida fica entre o snapshot e o commit gravado.
func TestRestartApplied(t *testing.T) {
	for _, tc := range []struct{ snap, commit, want, got uint64 }{
		{1, 100, 0, 1}, {1, 100, 50, 50}, {1, 100, 500, 100}, {10, 100, 5, 10},
	} {
		if g := restartApplied(tc.snap, tc.commit, tc.want); g != tc.got {
			t.Fatalf("restartApplied(%d, %d, %d) = %d, quer %d", tc.snap, tc.commit, tc.want, g, tc.got)
		}
	}
}

var _ port.ConsensusEngine = (*Node)(nil)

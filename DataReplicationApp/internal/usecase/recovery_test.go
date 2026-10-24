package usecase

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// fakeSource é uma célula vista pelo canal de recuperação.
type fakeSource struct {
	info    port.RetainedInfo
	down    bool // Retained falha
	failAt  int  // o stream cai depois de failAt lotes, e a fonte fica fora (0 = nunca)
	batch   int  // entradas por lote
	opened  int
	lastReq [2]domain.Index
}

// fakeRecoveryClient serve cada nó a partir de um log contíguo de 1 até o gate dele.
type fakeRecoveryClient struct {
	mu    sync.Mutex
	nodes map[uint64]*fakeSource
}

func (c *fakeRecoveryClient) Retained(_ context.Context, node uint64) (port.RetainedInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.nodes[node]
	if s == nil || s.down {
		return port.RetainedInfo{}, port.ErrSourceGone
	}
	return s.info, nil
}

func (c *fakeRecoveryClient) CatchUp(_ context.Context, node uint64, from, to domain.Index) (port.EntryStream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.nodes[node]
	if s == nil || s.down {
		return nil, port.ErrSourceGone
	}
	s.opened++
	s.lastReq = [2]domain.Index{from, to}
	return &fakeEntryStream{c: c, node: node, next: from, to: to}, nil
}

func (c *fakeRecoveryClient) CopyTable(context.Context, uint64, []domain.LogicalTable) (port.CopyStream, error) {
	return nil, errors.New("não usado")
}

func (c *fakeRecoveryClient) set(node uint64, f func(*fakeSource)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c.nodes[node])
}

func (c *fakeRecoveryClient) opened(node uint64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodes[node].opened
}

type fakeEntryStream struct {
	c        *fakeRecoveryClient
	node     uint64
	next, to domain.Index
	sent     int
}

func (s *fakeEntryStream) Recv() (port.CatchUpBatch, error) {
	s.c.mu.Lock()
	src := s.c.nodes[s.node]
	s.c.mu.Unlock()
	if s.next > s.to {
		return port.CatchUpBatch{}, io.EOF
	}
	if src.failAt > 0 && s.sent >= src.failAt {
		s.c.set(s.node, func(f *fakeSource) { f.down = true }) // a fonte caiu
		return port.CatchUpBatch{}, port.ErrSourceGone
	}
	n := max(src.batch, 1)
	var b port.CatchUpBatch
	for i := 0; i < n && s.next <= s.to; i++ {
		b.Entries = append(b.Entries, domain.Entry{Index: s.next, Term: 1})
		s.next++
	}
	b.SourceGate = src.info.Gate
	s.sent++
	return b, nil
}

func (s *fakeEntryStream) Close() {}

func normalSource(gate domain.Index, first domain.Index, leader bool) *fakeSource {
	return &fakeSource{info: port.RetainedInfo{State: "normal", FirstRetained: first, Gate: gate, Leader: leader}, batch: 4}
}

// waterApplier consome o canal do applier e mantém a marca d'água (contiguidade conferida).
type waterApplier struct {
	t       *testing.T
	mu      sync.Mutex
	applied domain.Index
	seen    []domain.Index
	changed chan struct{}
}

func newWaterApplier(t *testing.T, in <-chan []domain.Entry, start domain.Index) *waterApplier {
	a := &waterApplier{t: t, applied: start, changed: make(chan struct{})}
	go func() {
		for b := range in {
			a.mu.Lock()
			for _, e := range b {
				if e.Index != a.applied+1 {
					t.Errorf("buraco ou duplicata: aplicado %d, veio %d", a.applied, e.Index)
				}
				a.applied = e.Index
				a.seen = append(a.seen, e.Index)
			}
			close(a.changed)
			a.changed = make(chan struct{})
			a.mu.Unlock()
		}
	}()
	return a
}

func (a *waterApplier) Applied() domain.Index {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applied
}

func (a *waterApplier) WaitApplied(ctx context.Context, idx domain.Index) error {
	for {
		a.mu.Lock()
		if a.applied >= idx {
			a.mu.Unlock()
			return nil
		}
		ch := a.changed
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}

type recoveryRig struct {
	cons    *fakeConsensus
	gate    *domain.Gate
	mode    *domain.CellMode
	client  *fakeRecoveryClient
	rec     *Recovery
	applier *waterApplier
	pump    *CommitPump
}

// newRecoveryRig monta uma follower (nó 1) com o log local até localCommit.
func newRecoveryRig(t *testing.T, sources map[uint64]*fakeSource, localCommit domain.Index) *recoveryRig {
	t.Helper()
	r := &recoveryRig{cons: newFakeConsensus(), gate: &domain.Gate{}, mode: &domain.CellMode{},
		client: &fakeRecoveryClient{nodes: sources}}
	for i := domain.Index(1); i <= localCommit; i++ {
		r.cons.commitEntries(domain.Entry{Index: i, Term: 1})
	}
	r.gate.SetFollower(true)
	r.gate.SetReceived(1 << 40) // a líder já confirmou tudo
	toApply := make(chan []domain.Entry, 4)
	r.applier = newWaterApplier(t, toApply, 0)
	peers := []uint64{1}
	for id := range sources {
		peers = append(peers, id)
	}
	r.rec = &Recovery{Self: 1, Peers: peers, Client: r.client, Mode: r.mode, Gate: r.gate, Applier: r.applier,
		Retry: 20 * time.Millisecond, Log: zerolog.Nop()}
	r.pump = &CommitPump{Consensus: r.cons, Gate: r.gate, ToApplier: toApply, From: 1, Mode: r.mode, Remote: r.rec,
		Applied: r.applier, Log: zerolog.Nop()}
	return r
}

func (r *recoveryRig) run(t *testing.T) chan error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.pump.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return done
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("tempo esgotado esperando: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// AP-17: Behind põe em recuperando e o alvo só sobe; em reconstruindo, ignora.
func TestRecoveryBehind(t *testing.T) {
	r := newRecoveryRig(t, map[uint64]*fakeSource{2: normalSource(100, 1, false)}, 0)
	r.rec.Behind(50)
	r.rec.Behind(30)
	if r.mode.State() != domain.CellRecovering || r.mode.Target() != 50 {
		t.Fatalf("estado %v alvo %d", r.mode.State(), r.mode.Target())
	}
	r.mode.EnterRebuilding()
	r.rec.Behind(80)
	if r.mode.State() != domain.CellRebuilding {
		t.Fatal("em reconstruindo, Behind não muda nada")
	}
}

// AP-17: a escolha prefere a follower de maior gate, ignora a fonte fora de normal e a
// que não retém.
func TestRecoveryPickSource(t *testing.T) {
	leader := normalSource(100, 1, true)
	notNormal := normalSource(100, 1, false)
	notNormal.info.State = "recuperando"
	lowGate := normalSource(60, 1, false)
	noRetain := normalSource(100, 40, false)
	best := normalSource(90, 1, false)
	r := newRecoveryRig(t, map[uint64]*fakeSource{2: leader, 3: notNormal, 4: lowGate, 5: noRetain, 6: best}, 0)
	src, err := r.rec.pickSource(context.Background(), 30)
	if err != nil || src != 6 {
		t.Fatalf("fonte = %d %v, quer 6 (follower de maior gate)", src, err)
	}
	r.client.set(6, func(s *fakeSource) { s.down = true })
	r.client.set(4, func(s *fakeSource) { s.down = true })
	if src, _ := r.rec.pickSource(context.Background(), 30); src != 2 {
		t.Fatalf("sem follower elegível, a líder serve: %d", src)
	}
}

// AP-18: nenhuma fonte retém from → ErrNoSource → o pump põe a célula em reconstruindo.
func TestRecoveryNoSourceGoesRebuilding(t *testing.T) {
	r := newRecoveryRig(t, map[uint64]*fakeSource{2: normalSource(100, 50, true), 3: normalSource(100, 50, false)}, 0)
	r.run(t)
	r.rec.Behind(80)
	waitFor(t, "reconstruindo", func() bool { return r.mode.State() == domain.CellRebuilding })
}

// DC-02: com o commit local parado, só o SetRecovered do CatchUp libera o gate da follower.
func TestRecoveryGateFollowsRecovered(t *testing.T) {
	g := &domain.Gate{}
	g.SetFollower(true)
	g.SetRaftCommit(5) // commit local parado
	g.SetReceived(100)
	if g.Index() != 5 {
		t.Fatalf("sem o recuperado, o gate fica no commit local: %d", g.Index())
	}
	r := newRecoveryRig(t, map[uint64]*fakeSource{2: normalSource(100, 1, false)}, 0)
	r.rec.Gate = g
	r.mode.EnterRecovering(40)
	ents, err := r.rec.Next(context.Background(), 6)
	if err != nil || len(ents) == 0 {
		t.Fatalf("Next: %v %v", ents, err)
	}
	if g.Index() != ents[len(ents)-1].Index {
		t.Fatalf("gate = %d, quer %d (o fim do lote)", g.Index(), ents[len(ents)-1].Index)
	}
}

// DC-03: o pump sai do log local para a fonte remota e volta a normal, sem buraco nem
// duplicata; o alvo que sobe durante a recuperação é seguido.
func TestCommitPumpLocalRemoteAndBack(t *testing.T) {
	r := newRecoveryRig(t, map[uint64]*fakeSource{2: normalSource(1000, 1, false)}, 10)
	r.run(t)
	waitFor(t, "local até 10", func() bool { return r.applier.Applied() == 10 })
	r.rec.Behind(40)
	waitFor(t, "alvo 40 aplicado", func() bool { return r.applier.Applied() >= 40 })
	r.rec.Behind(70) // o alvo sobe (um marcador mais novo)
	waitFor(t, "normal depois de 70", func() bool {
		return r.mode.State() == domain.CellNormal && r.applier.Applied() == 70
	})
	if r.applier.Applied() != 70 {
		t.Fatalf("aplicado %d", r.applier.Applied())
	}
	// De volta ao log local: o marcador foi aceito e o log segue de 71.
	for i := domain.Index(11); i <= 75; i++ {
		r.cons.commitEntries(domain.Entry{Index: i, Term: 1})
	}
	waitFor(t, "local até 75", func() bool { return r.applier.Applied() == 75 })
}

// AP-17: a fonte cai no meio; a recuperação troca de fonte e continua de onde parou.
func TestRecoverySwapsSourceMidway(t *testing.T) {
	flaky := normalSource(1000, 1, false)
	flaky.failAt = 2
	other := normalSource(500, 1, false)
	r := newRecoveryRig(t, map[uint64]*fakeSource{2: flaky, 3: other}, 0)
	r.run(t)
	r.rec.Behind(60)
	waitFor(t, "alcançou 60", func() bool { return r.applier.Applied() >= 60 })
	waitFor(t, "normal", func() bool { return r.mode.State() == domain.CellNormal })
	if r.client.opened(3) == 0 {
		t.Fatal("devia ter trocado para a fonte 3")
	}
}

// AP-16: fora de normal, a leitura forte falha com ErrCellRecovering antes da barreira.
func TestReadRefusedOutsideNormal(t *testing.T) {
	mode := &domain.CellMode{}
	st := &readStore{}
	s := &ReadService{Consensus: newFakeConsensus(), Gate: &domain.Gate{}, Applied: appliedOK(), Store: st, Scanner: st, Mode: mode}
	mode.EnterRecovering(10)
	if _, err := s.Get(context.Background(), "t", []byte(`{}`)); !errors.Is(err, ErrCellRecovering) {
		t.Fatalf("Get = %v", err)
	}
	if _, err := s.Scan(context.Background(), "t", 10, nil); !errors.Is(err, ErrCellRecovering) {
		t.Fatalf("Scan = %v", err)
	}
	if st.calls != 0 {
		t.Fatal("o banco local não devia ser lido")
	}
}

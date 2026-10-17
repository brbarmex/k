package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// readConsensus é o motor falso com ReadIndex controlável.
type readConsensus struct {
	*fakeConsensus
	ri domain.Index
}

func (r *readConsensus) ReadIndex(context.Context) (domain.Index, error) { return r.ri, nil }

// recApplied registra até onde a leitura pediu o apply.
type recApplied struct {
	mu      sync.Mutex
	asked   domain.Index
	applied domain.Index
}

func (a *recApplied) Applied() domain.Index {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applied
}

func (a *recApplied) WaitApplied(ctx context.Context, idx domain.Index) error {
	a.mu.Lock()
	a.asked = idx
	ok := a.applied >= idx
	a.mu.Unlock()
	if ok {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

type readStore struct{ calls int }

func (s *readStore) ApplyBatch(context.Context, []port.VersionedOp) error { return nil }
func (s *readStore) Get(context.Context, domain.LogicalTable, []byte) ([]byte, error) {
	s.calls++
	return []byte(`{"pk":{"S":"a"}}`), nil
}
func (s *readStore) Scan(context.Context, domain.LogicalTable, int, []byte) (port.ScanPage, error) {
	s.calls++
	return port.ScanPage{Items: [][]byte{[]byte(`{}`)}}, nil
}

type fakeBarrierClient struct {
	ri, gate domain.Index
	err      error
	calls    int
}

func (f *fakeBarrierClient) Barrier(context.Context, string) (domain.Index, domain.Index, error) {
	f.calls++
	return f.ri, f.gate, f.err
}

func newRead(leaderLocal bool, ri domain.Index, gate *domain.Gate, ap *recApplied, bc *fakeBarrierClient) (*ReadService, *readStore) {
	fc := newFakeConsensus()
	fc.setLeader(port.LeaderInfo{NodeID: "2", Local: leaderLocal}, true)
	st := &readStore{}
	return &ReadService{Consensus: &readConsensus{fakeConsensus: fc, ri: ri}, Gate: gate, Applied: ap, Barrier: bc, Store: st, Scanner: st}, st
}

// Na líder: gate pronto, ReadIndex, e espera o apply até min(ReadIndex, gate).
func TestLeaderReadWaitsMinOfReadIndexAndGate(t *testing.T) {
	g := &domain.Gate{}
	g.SetRaftCommit(8)
	ap := &recApplied{applied: 8}
	s, st := newRead(true, 10, g, ap, &fakeBarrierClient{})
	m := &recMetrics{}
	s.Metrics = m
	if _, err := s.Get(context.Background(), "t_{cell}", []byte("k")); err != nil {
		t.Fatal(err)
	}
	if m.n("barrier") != 1 {
		t.Fatalf("barreiras registradas = %d, quer 1", m.n("barrier"))
	}
	if ap.asked != 8 || st.calls != 1 {
		t.Fatalf("pediu apply até %d (quer 8), leituras %d", ap.asked, st.calls)
	}
}

// stoppedApplied simula o applier estacionado num item venenoso (ADR-038).
type stoppedApplied struct{}

func (stoppedApplied) Applied() domain.Index                           { return 0 }
func (stoppedApplied) WaitApplied(context.Context, domain.Index) error { return ErrApplierStopped }

// ADR-049, item 4: com o applier estacionado, a leitura forte falha na hora com
// ErrApplierStopped, e não com READ_TIMEOUT depois do prazo; o banco não é lido.
func TestReadFailsFastWhenApplierIsStopped(t *testing.T) {
	g := &domain.Gate{}
	g.SetRaftCommit(8)
	s, st := newRead(true, 8, g, &recApplied{}, &fakeBarrierClient{})
	s.Applied = stoppedApplied{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := s.Get(ctx, "t_{cell}", []byte("k")); !errors.Is(err, ErrApplierStopped) {
		t.Fatalf("GetItem = %v, quer ErrApplierStopped", err)
	}
	if _, err := s.Scan(ctx, "t_{cell}", 0, nil); !errors.Is(err, ErrApplierStopped) {
		t.Fatalf("Scan = %v, quer ErrApplierStopped", err)
	}
	if time.Since(start) > time.Second || st.calls != 0 {
		t.Fatalf("levou %v e leu o banco %d vezes; devia falhar na hora sem ler", time.Since(start), st.calls)
	}
}

// Na follower: usa a barreira da líder, nunca o gate local.
func TestFollowerReadUsesLeaderBarrier(t *testing.T) {
	g := &domain.Gate{}
	g.SetRaftCommit(100) // gate local alto não pode ser usado
	ap := &recApplied{applied: 50}
	bc := &fakeBarrierClient{ri: 7, gate: 6}
	s, _ := newRead(false, 0, g, ap, bc)
	if _, err := s.Scan(context.Background(), "t_{cell}", 0, nil); err != nil {
		t.Fatal(err)
	}
	if bc.calls != 1 || ap.asked != 6 {
		t.Fatalf("barreiras %d, pediu apply até %d (quer 6)", bc.calls, ap.asked)
	}
	// Uma barreira por chamada.
	_, _ = s.Scan(context.Background(), "t_{cell}", 0, nil)
	if bc.calls != 2 {
		t.Fatalf("cada página precisa da sua barreira: %d", bc.calls)
	}
}

// Apply local atrasado vira ErrReadTimeout; sem líder vira ErrNoLeader.
func TestReadTimeoutAndNoLeader(t *testing.T) {
	g := &domain.Gate{}
	g.SetRaftCommit(5)
	s, _ := newRead(true, 5, g, &recApplied{applied: 1}, &fakeBarrierClient{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Get(ctx, "t_{cell}", nil); !errors.Is(err, ErrReadTimeout) {
		t.Fatalf("erro = %v, quer ErrReadTimeout", err)
	}
	s.Consensus.(*readConsensus).setLeader(port.LeaderInfo{}, false)
	if _, err := s.Get(context.Background(), "t_{cell}", nil); !errors.Is(err, ErrNoLeader) {
		t.Fatalf("erro = %v, quer ErrNoLeader", err)
	}
	if _, _, err := s.LeaderBarrier(context.Background()); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("barreira sem liderança = %v", err)
	}
}

type recPublisher struct {
	mu   sync.Mutex
	last domain.Index
	n    int
}

func (p *recPublisher) Publish(g domain.Index) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last, p.n = g, p.n+1
}

func (p *recPublisher) get() (domain.Index, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last, p.n
}

// G-06: a líder publica a cada avanço e periodicamente; a follower não passa do commit local.
func TestGateSync(t *testing.T) {
	fc := newFakeConsensus()
	g := &domain.Gate{}
	pub := &recPublisher{}
	s := &GateSync{Consensus: fc, Gate: g, Publisher: pub, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	g.SetRaftCommit(4)
	waitUntil(t, func() bool { last, _ := pub.get(); return last == 4 }, "a líder não publicou o avanço")
	_, n := pub.get()
	waitUntil(t, func() bool { _, m := pub.get(); return m > n+2 }, "a líder não republicou no intervalo")

	fc.setLeader(port.LeaderInfo{NodeID: "2"}, true) // vira follower
	waitUntil(t, func() bool {                       // follower não publica: o papel foi atualizado
		_, m := pub.get()
		time.Sleep(20 * time.Millisecond)
		_, m2 := pub.get()
		return m == m2
	}, "o papel não foi atualizado")
	g.SetRaftCommit(10)
	s.OnGateUpdate(20)
	if g.Index() != 10 {
		t.Fatalf("follower: gate = %d, quer 10 (commit local limita)", g.Index())
	}
	cancel()
	<-done
}

func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

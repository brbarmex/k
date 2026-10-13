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

type fakeStore struct {
	mu      sync.Mutex
	applied []port.VersionedOp
	failAt  domain.Index
}

func (s *fakeStore) ApplyBatch(_ context.Context, ops []port.VersionedOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range ops {
		if op.Version == s.failAt {
			return errors.New("item venenoso")
		}
		s.applied = append(s.applied, op)
	}
	return nil
}
func (*fakeStore) Get(context.Context, domain.LogicalTable, []byte, bool) ([]byte, error) {
	return nil, nil
}
func (*fakeStore) AppliedIndex() domain.Index { return 0 }
func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.applied)
}

type fakeProgress struct {
	mu    sync.Mutex
	idx   domain.Index
	ord   uint32
	saves int
}

func (p *fakeProgress) SaveProgress(i domain.Index, o uint32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idx, p.ord = i, o
	p.saves++
	return nil
}
func (p *fakeProgress) LoadProgress() (domain.Index, uint32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idx, p.ord, nil
}

func write(idx domain.Index, items int) domain.Entry {
	e := domain.Entry{Index: idx, Term: 1, Kind: domain.KindWrite}
	for i := 0; i < items; i++ {
		e.Ops = append(e.Ops, domain.Op{Table: "t_{cell}", Kind: domain.OpPut, Key: []byte{byte(i)}})
	}
	return e
}

func startApplier(t *testing.T, a *Applier) (context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	return cancel, done
}

func TestApplierNeverPassesTheGate(t *testing.T) {
	g := &domain.Gate{}
	in := make(chan []domain.Entry, 1)
	st := &fakeStore{}
	a := &Applier{Gate: g, Store: st, Progress: &fakeProgress{}, In: in}
	cancel, _ := startApplier(t, a)
	defer cancel()

	in <- []domain.Entry{write(1, 1), write(2, 1)}
	time.Sleep(20 * time.Millisecond)
	if st.count() != 0 {
		t.Fatalf("aplicou %d itens antes do gate", st.count())
	}
	releaseGate(g, 1, 1)
	ctx, c2 := context.WithTimeout(context.Background(), time.Second)
	defer c2()
	if err := a.WaitApplied(ctx, 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if a.Applied() != 1 || st.count() != 1 {
		t.Fatalf("applied = %d, itens = %d; o gate estava em 1", a.Applied(), st.count())
	}
	releaseGate(g, 1, 2)
	if err := a.WaitApplied(ctx, 2); err != nil {
		t.Fatal(err)
	}
}

func TestApplierResumesFromItemProgress(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 1, 10)
	in := make(chan []domain.Entry, 1)
	st := &fakeStore{}
	// Antes da queda, a entrada 5 tinha os itens 1 e 2 de 3 aplicados.
	prog := &fakeProgress{idx: 5, ord: 2}
	a := &Applier{Gate: g, Store: st, Progress: prog, In: in}
	cancel, _ := startApplier(t, a)
	defer cancel()

	in <- []domain.Entry{write(4, 2), write(5, 3), write(6, 1)}
	ctx, c2 := context.WithTimeout(context.Background(), time.Second)
	defer c2()
	if err := a.WaitApplied(ctx, 6); err != nil {
		t.Fatal(err)
	}
	// Reaplica só (5, 3) e (6, 1): 4 e os dois primeiros itens de 5 já estavam no banco.
	if st.count() != 2 {
		t.Fatalf("itens reaplicados = %d, quer 2", st.count())
	}
	if idx, ord, _ := prog.LoadProgress(); idx != 6 || ord != 1 {
		t.Fatalf("progresso = (%d, %d), quer (6, 1)", idx, ord)
	}
}

func TestPoisonItemStopsTheShard(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 1, 10)
	in := make(chan []domain.Entry, 1)
	st := &fakeStore{failAt: 2}
	a := &Applier{Gate: g, Store: st, Progress: &fakeProgress{}, In: in}
	_, done := startApplier(t, a)

	in <- []domain.Entry{write(1, 1), write(2, 1), write(3, 1)}
	select {
	case err := <-done:
		if err == nil || a.Applied() != 1 {
			t.Fatalf("erro = %v, applied = %d; devia parar em 2 sem pular", err, a.Applied())
		}
	case <-time.After(time.Second):
		t.Fatal("applier não parou no item venenoso")
	}
}

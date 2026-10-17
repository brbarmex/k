package usecase

import (
	"context"
	"sync"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// fakeConsensus é um motor de consenso controlável pelos testes. Seguro para uso concorrente.
type fakeConsensus struct {
	mu         sync.Mutex
	next       domain.Index // último índice entregue
	leader     port.LeaderInfo
	hasLeader  bool
	term       domain.Term
	proposed   []domain.Entry
	onPropose  func(domain.Entry) // gancho executado dentro do Propose (sem o mutex)
	proposeErr error

	committed chan port.CommitBatch
	events    chan port.LearnerProgress
}

func newFakeConsensus() *fakeConsensus {
	return &fakeConsensus{
		hasLeader: true, leader: port.LeaderInfo{NodeID: "n1", Local: true}, term: 1, next: 4,
		committed: make(chan port.CommitBatch), events: make(chan port.LearnerProgress),
	}
}

func (f *fakeConsensus) Propose(_ context.Context, e domain.Entry) (domain.Index, error) {
	f.mu.Lock()
	hook, perr := f.onPropose, f.proposeErr
	f.mu.Unlock()
	if hook != nil {
		hook(e)
	}
	if perr != nil {
		return 0, perr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	e.Index = f.next
	f.proposed = append(f.proposed, e)
	return f.next, nil
}

func (f *fakeConsensus) setLeader(l port.LeaderInfo, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leader, f.hasLeader = l, ok
}

func (f *fakeConsensus) count(k domain.EntryKind) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.proposed {
		if e.Kind == k {
			n++
		}
	}
	return n
}

func (f *fakeConsensus) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.proposed)
}

func (f *fakeConsensus) entries() []domain.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Entry(nil), f.proposed...)
}

func (f *fakeConsensus) ReadIndex(context.Context) (domain.Index, error) { return 0, nil }
func (f *fakeConsensus) Leader() (port.LeaderInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leader, f.hasLeader
}
func (f *fakeConsensus) TransferLeadership(context.Context, string) error { return nil }
func (f *fakeConsensus) Committed() <-chan port.CommitBatch               { return f.committed }
func (f *fakeConsensus) LearnerEvents() <-chan port.LearnerProgress       { return f.events }
func (f *fakeConsensus) LearnerDurable() []port.LearnerProgress           { return nil }
func (f *fakeConsensus) Term() domain.Term {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.term
}
func (f *fakeConsensus) setTerm(t domain.Term) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.term = t
}

// fakeApplied: bloqueia até done fechar; onWait roda antes (simula pausa de processo).
type fakeApplied struct {
	done   chan struct{}
	onWait func()
}

func (f fakeApplied) WaitApplied(ctx context.Context, _ domain.Index) error {
	if f.onWait != nil {
		f.onWait()
	}
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func appliedOK() fakeApplied {
	d := make(chan struct{})
	close(d)
	return fakeApplied{done: d}
}

type fakeForwarder struct {
	mu    sync.Mutex
	calls []port.WriteRequest
}

func (f *fakeForwarder) Forward(_ context.Context, _ string, r port.WriteRequest) (port.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	return port.WriteResult{Index: 99}, nil
}

// fakeClock é o relógio injetável do lease.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(2_000_000, 0)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// releaseGate libera o gate até idx no termo term, como se Raft e Azure tivessem confirmado.
func releaseGate(g *domain.Gate, term domain.Term, idx domain.Index) {
	g.SetTerm(term)
	g.SetRaftCommit(idx)
	g.SetAzureDurable(term, idx, "az1")
	g.SetAzureDurable(term, idx, "az2")
}

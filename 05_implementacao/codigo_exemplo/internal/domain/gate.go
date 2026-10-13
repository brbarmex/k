package domain

import (
	"context"
	"sort"
	"sync"
)

// Gate calcula até onde o ACK e o applier podem avançar:
// min(commit do Raft, índice durável em ≥ 2 de 3 learners da Azure).
// O valor nunca recua (D9).
type Gate struct {
	mu          sync.Mutex
	raftCommit  Index
	azureQuorum Index
	current     Index
	waiters     []waiter
	degraded    bool // em modo degradado a Azure sai da conta
}

type waiter struct {
	idx Index
	ch  chan struct{}
}

func (g *Gate) Index() Index {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current
}

func (g *Gate) SetRaftCommit(i Index) { g.update(func() { g.raftCommit = max(g.raftCommit, i) }) }

// SetAzureDurable recebe o índice durável de cada learner (3 valores) e guarda o 2º maior.
func (g *Gate) SetAzureDurable(durable [3]Index) {
	d := durable[:]
	sort.Slice(d, func(a, b int) bool { return d[a] > d[b] })
	g.update(func() { g.azureQuorum = max(g.azureQuorum, d[1]) })
}

func (g *Gate) SetDegraded(on bool) { g.update(func() { g.degraded = on }) }

func (g *Gate) update(mutate func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	mutate()
	next := g.raftCommit
	if !g.degraded {
		next = min(next, g.azureQuorum)
	}
	g.current = max(g.current, next) // monotônico
	kept := g.waiters[:0]
	for _, w := range g.waiters {
		if w.idx <= g.current {
			close(w.ch)
		} else {
			kept = append(kept, w)
		}
	}
	g.waiters = kept
}

// WaitFor bloqueia até o gate alcançar idx ou o contexto expirar.
func (g *Gate) WaitFor(ctx context.Context, idx Index) error {
	g.mu.Lock()
	if g.current >= idx {
		g.mu.Unlock()
		return nil
	}
	w := waiter{idx: idx, ch: make(chan struct{})}
	g.waiters = append(g.waiters, w)
	g.mu.Unlock()
	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

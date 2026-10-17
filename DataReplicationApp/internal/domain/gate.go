package domain

import (
	"context"
	"sync"
)

// Gate calcula até onde o ACK e o applier podem avançar.
//
// Regras (ADR-029):
//   - o valor nunca recua (monotônico), nem na troca de líder;
//   - na célula líder o gate é o commit do Raft;
//   - numa célula follower o gate é min(commit local, maior gate recebido da líder)
//     (G-06): um gate recebido é um valor já provado e o commit local continua limitando.
//
// O valor zero é utilizável (papel de líder). Todos os métodos são seguros para uso
// concorrente.
type Gate struct {
	mu         sync.Mutex
	raftCommit Index
	current    Index
	waiters    map[*waiter]struct{}
	received   Index         // maior gate recebido da líder (G-06)
	follower   bool          // a célula não é a líder: o gate segue o recebido
	changed    chan struct{} // fechado e trocado a cada avanço de current
}

// GateState é um retrato do gate para o /status e os testes.
type GateState struct {
	Index      Index
	RaftCommit Index
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

// SetReceived registra um gate publicado pela líder (G-06). Adota o máximo: um gate
// recebido é um valor já provado. Na follower o commit local continua limitando.
func (g *Gate) SetReceived(i Index) { g.update(func() { g.received = max(g.received, i) }) }

// SetFollower informa o papel da célula. Na follower o gate segue o recebido da líder;
// na líder, o commit do Raft. O valor já liberado não recua.
func (g *Gate) SetFollower(on bool) { g.update(func() { g.follower = on }) }

// Changed devolve um canal fechado no próximo avanço do gate.
func (g *Gate) Changed() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	return g.changed
}

// Snapshot devolve um retrato coerente do gate.
func (g *Gate) Snapshot() GateState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return GateState{Index: g.current, RaftCommit: g.raftCommit}
}

func (g *Gate) update(mutate func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	mutate()
	next := g.raftCommit
	if g.follower {
		next = min(next, g.received) // G-06
	}
	prev := g.current
	g.current = max(g.current, next) // monotônico
	if g.current > prev && g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
	for w := range g.waiters {
		if w.idx <= g.current {
			close(w.ch)
			delete(g.waiters, w)
		}
	}
}

// WaitFor bloqueia até o gate alcançar idx ou o contexto expirar. Quando o contexto
// expira o waiter é removido: sob carga, as requisições não deixam registros para trás.
func (g *Gate) WaitFor(ctx context.Context, idx Index) error {
	g.mu.Lock()
	if g.current >= idx {
		g.mu.Unlock()
		return nil
	}
	w := &waiter{idx: idx, ch: make(chan struct{})}
	if g.waiters == nil {
		g.waiters = make(map[*waiter]struct{})
	}
	g.waiters[w] = struct{}{}
	g.mu.Unlock()

	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		g.mu.Lock()
		defer g.mu.Unlock()
		if _, still := g.waiters[w]; !still {
			return nil // foi liberado no mesmo instante em que o contexto expirou
		}
		delete(g.waiters, w)
		return ctx.Err()
	}
}

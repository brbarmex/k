package domain

import (
	"context"
	"sort"
	"sync"
)

// MaxLearners é o número de learners da Azure por shard (1 por AZ).
const MaxLearners = 3

// Gate calcula até onde o ACK e o applier podem avançar:
//
//	min(commit do Raft, índice durável em >= 2 de 3 learners da Azure).
//
// Regras (ADR-029 e ADR-046):
//   - o valor nunca recua (monotônico), nem na troca de líder;
//   - o progresso de um learner só vale se vier etiquetado com o TERMO ATUAL da
//     líder; progresso de outro termo (sufixo de uma líder antiga, número vindo de
//     fora de banda) é ignorado;
//   - quando o termo avança, o progresso dos learners é descartado e só volta a
//     contar depois da primeira confirmação no termo novo (reconstrução do gate);
//     o valor já liberado (current) não recua;
//   - numa célula follower o gate é min(commit local, maior gate recebido da líder)
//     (G-06): um gate recebido é um valor já provado e o commit local continua limitando;
//   - no modo sem Azure (G-08, SÓ DA POC) a líder usa gate = commit do Raft.
//
// O valor zero é o comportamento de produção: papel de líder e Azure exigida.
//
// O valor zero é utilizável. Todos os métodos são seguros para uso concorrente.
type Gate struct {
	mu          sync.Mutex
	term        Term
	raftCommit  Index
	learners    map[string]Index // progresso por learner, SÓ no termo atual
	azureQuorum Index
	current     Index
	waiters     map[*waiter]struct{}
	received    Index         // maior gate recebido da líder (G-06)
	follower    bool          // a célula não é a líder: o gate segue o recebido
	azureOff    bool          // SÓ DA POC (G-08): sem learners, gate = commit do Raft
	changed     chan struct{} // fechado e trocado a cada avanço de current
}

// GateState é um retrato do gate para o /status e os testes.
type GateState struct {
	Index      Index
	Term       Term
	RaftCommit Index
	Ready      bool
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

// SetTerm informa o termo atual da líder. Termo menor ou igual ao atual é ignorado.
// Termo maior descarta o progresso dos learners: a líder nova só adota o que for
// confirmado no termo novo (ADR-046, item 3). O índice já liberado não recua.
func (g *Gate) SetTerm(t Term) {
	g.update(func() {
		if t > g.term {
			g.term = t
			g.learners = nil
			g.azureQuorum = 0
		}
	})
}

func (g *Gate) SetRaftCommit(i Index) { g.update(func() { g.raftCommit = max(g.raftCommit, i) }) }

// SetAzureDurable registra o índice durável de UM learner, vindo da resposta do
// AppendEntries (ADR-046). Devolve true se o valor foi adotado. Não é adotado quando
// o termo difere do atual, quando o learner é um quarto nome distinto ou quando o valor
// não avança o do próprio learner.
func (g *Gate) SetAzureDurable(term Term, idx Index, learner string) bool {
	adopted := false
	g.update(func() {
		if term != g.term || learner == "" {
			return
		}
		if _, known := g.learners[learner]; !known && len(g.learners) >= MaxLearners {
			return
		}
		if g.learners == nil {
			g.learners = make(map[string]Index, MaxLearners)
		}
		if old, known := g.learners[learner]; known && idx <= old {
			return
		}
		g.learners[learner] = idx
		g.azureQuorum = g.quorumLocked()
		adopted = true
	})
	return adopted
}

// quorumLocked devolve o 2º maior índice entre os learners que já confirmaram no termo
// atual, ou 0 enquanto menos de 2 confirmaram.
func (g *Gate) quorumLocked() Index {
	if len(g.learners) < 2 {
		return 0
	}
	d := make([]Index, 0, len(g.learners))
	for _, v := range g.learners {
		d = append(d, v)
	}
	sort.Slice(d, func(a, b int) bool { return d[a] > d[b] })
	return d[1]
}

// Ready diz se o gate foi reconstruído no termo atual: pelo menos 2 learners
// confirmaram nele (ou a Azure está desligada na POC). A leitura
// forte da líder nova só começa depois disso (ADR-049, G-07).
func (g *Gate) Ready() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.readyLocked()
}

func (g *Gate) readyLocked() bool {
	return g.azureOff || len(g.learners) >= 2
}

// SetReceived registra um gate publicado pela líder (G-06). Adota o máximo: um gate
// recebido é um valor já provado. Na follower o commit local continua limitando.
func (g *Gate) SetReceived(i Index) { g.update(func() { g.received = max(g.received, i) }) }

// SetFollower informa o papel da célula. Na follower o gate segue o recebido da líder;
// na líder, a maioria Azure (ou o commit, sem Azure). O valor já liberado não recua.
func (g *Gate) SetFollower(on bool) { g.update(func() { g.follower = on }) }

// SetAzureOff liga o modo sem Azure.
//
// SÓ DA POC (G-08, SG-12): com a Azure desligada o ACK vira o commit AWS e o RPO 0 entre
// nuvens deixa de existir. Nunca ligar em produção.
func (g *Gate) SetAzureOff(on bool) { g.update(func() { g.azureOff = on }) }

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
	return GateState{
		Index: g.current, Term: g.term, RaftCommit: g.raftCommit, Ready: g.readyLocked(),
	}
}

func (g *Gate) update(mutate func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	mutate()
	next := g.raftCommit
	switch {
	case g.follower:
		next = min(next, g.received) // G-06
	case !g.azureOff:
		next = min(next, g.azureQuorum) // G-01
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
// expira o waiter é removido: sob carga com a Azure fora, as requisições não deixam
// registros para trás.
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

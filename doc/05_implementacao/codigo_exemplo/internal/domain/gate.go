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
//   - em modo degradado a Azure sai da conta (a validade do lease é conferida por
//     quem ACKa, ver usecase.DegradeService).
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

// Term devolve o termo atual conhecido pelo gate.
func (g *Gate) Term() Term {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.term
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
// o termo difere do atual, quando o learner é um quarto nome distinto (troca de membro
// passa por ForgetLearner) ou quando o valor não avança o do próprio learner.
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

// ForgetLearner remove um learner que saiu do conjunto (disco trocado, identidade nova,
// ADR-034). O valor já liberado do gate não recua.
func (g *Gate) ForgetLearner(learner string) {
	g.update(func() {
		delete(g.learners, learner)
		g.azureQuorum = g.quorumLocked()
	})
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
// confirmaram nele (ou o modo é degradado). A leitura forte da líder nova só começa
// depois disso (ADR-049).
func (g *Gate) Ready() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.degraded || len(g.learners) >= 2
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
	for w := range g.waiters {
		if w.idx <= g.current {
			close(w.ch)
			delete(g.waiters, w)
		}
	}
}

// Waiting devolve quantos waiters estão registrados (diagnóstico e testes de vazamento).
func (g *Gate) Waiting() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.waiters)
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

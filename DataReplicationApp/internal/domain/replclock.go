package domain

import (
	"sort"
	"sync"
	"time"
)

const (
	// ReplClockCapacity é quantas amostras o relógio de replicação guarda (DC-16): com uma
	// amostra a cada 10 ms, cerca de 80 s de horizonte sob carga contínua.
	ReplClockCapacity = 8192
	// ReplClockMinGap é o intervalo mínimo entre duas amostras.
	ReplClockMinGap = 10 * time.Millisecond
)

// ReplSample é uma amostra do log da líder: o último índice de um Append, o instante em que
// ele entrou no log e o instante em que foi commitado (zero = ainda não commitado). A
// amostra representa as entradas depois da amostra anterior até Index.
type ReplSample struct {
	Index     Index
	Appended  time.Time
	Committed time.Time
}

// ReplicationClock guarda, na líder, amostras de (índice, entrada no log, commit) num buffer
// circular de tamanho fixo (DC-16). É a base do atraso de replicação em tempo (M-11) e das
// etapas do tempo de replicação (D-04), sempre no relógio da líder: nenhum instante de
// outra célula entra no cálculo.
//
// O valor zero é utilizável. Todos os métodos são seguros para uso concorrente; os de
// gravação não alocam (são chamados do laço do Ready, como o ramTracker).
type ReplicationClock struct {
	mu   sync.Mutex
	ring [ReplClockCapacity]ReplSample
	head int // posição da amostra mais antiga
	size int
	// commitPos: quantas amostras, a partir da mais antiga, já estão commitadas.
	commitPos int
	// from é o primeiro índice coberto pela amostra mais antiga.
	from  Index
	since time.Time // instante do último Reset
}

func (c *ReplicationClock) at(i int) *ReplSample { return &c.ring[(c.head+i)%ReplClockCapacity] }

// Reset descarta todas as amostras: a célula acabou de virar líder e passa a medir a partir
// de at.
func (c *ReplicationClock) Reset(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.head, c.size, c.commitPos, c.from, c.since = 0, 0, 0, 0, at
}

// RecordAppend registra o último índice acrescentado ao log da líder. Um índice que não
// passa da amostra mais nova (reescrita da cauda não commitada) ou que chega menos de
// ReplClockMinGap depois dela não cria amostra.
func (c *ReplicationClock) RecordAppend(last Index, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size > 0 {
		newest := c.at(c.size - 1)
		if last <= newest.Index || at.Sub(newest.Appended) < ReplClockMinGap {
			return
		}
	}
	if c.size == ReplClockCapacity { // descarta a mais antiga
		c.from = c.at(0).Index + 1
		c.head = (c.head + 1) % ReplClockCapacity
		c.size--
		if c.commitPos > 0 {
			c.commitPos--
		}
	}
	if c.size == 0 && c.from == 0 {
		c.from = last
	}
	*c.at(c.size) = ReplSample{Index: last, Appended: at}
	c.size++
}

// RecordCommit marca como commitadas, em at, as amostras com índice ≤ commit. O custo é o
// das amostras novas.
func (c *ReplicationClock) RecordCommit(commit Index, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.commitPos < c.size {
		s := c.at(c.commitPos)
		if s.Index > commit {
			return
		}
		s.Committed = at
		c.commitPos++
	}
}

// Delay devolve o atraso de replicação de uma célula com o aplicado dado: a idade, em now,
// do commit da entrada commitada mais antiga que ela não tem. saturado = a entrada é mais
// antiga que a amostra mais antiga, e o valor é um mínimo.
func (c *ReplicationClock) Delay(applied, commit Index, now time.Time) (time.Duration, bool) {
	if applied >= commit {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size == 0 {
		return nonNeg(now.Sub(c.since)), true
	}
	if applied+1 < c.from { // antes da janela das amostras
		return nonNeg(now.Sub(c.at(0).Appended)), true
	}
	k := sort.Search(c.size, func(i int) bool { return c.at(i).Index > applied })
	if k == c.size {
		// As entradas que faltam entraram no log depois da amostra mais nova, a menos de
		// ReplClockMinGap dela: a idade é no mínimo a da amostra menos esse intervalo.
		return nonNeg(now.Sub(c.at(c.size-1).Appended) - ReplClockMinGap), false
	}
	if s := c.at(k); !s.Committed.IsZero() {
		return nonNeg(now.Sub(s.Committed)), false
	}
	return 0, false
}

// Between devolve uma cópia das amostras com after < Index ≤ upTo, em ordem.
func (c *ReplicationClock) Between(after, upTo Index) []ReplSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	if upTo <= after {
		return nil
	}
	k := sort.Search(c.size, func(i int) bool { return c.at(i).Index > after })
	var out []ReplSample
	for ; k < c.size; k++ {
		s := c.at(k)
		if s.Index > upTo {
			break
		}
		out = append(out, *s)
	}
	return out
}

// Horizon é a idade da amostra mais antiga (0 sem amostras).
func (c *ReplicationClock) Horizon(now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size == 0 {
		return 0
	}
	return nonNeg(now.Sub(c.at(0).Appended))
}

func nonNeg(d time.Duration) time.Duration { return max(d, 0) }

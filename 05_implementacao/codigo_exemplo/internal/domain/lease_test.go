package domain

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_000_000, 0)} }
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

// majorityContact simula heartbeats confirmados por 2 de 3 learners, enviados em at.
func majorityContact(l *Lease, term Term, at time.Time) {
	l.SetTerm(term)
	l.ObserveContact(term, "az1", at)
	l.ObserveContact(term, "az2", at)
}

// A checagem é now < t_c + D, com t_c o último contato com a MAIORIA Azure.
func TestLeaseExpiresAtDFromLastContact(t *testing.T) {
	clk := newFakeClock()
	var l Lease
	if err := l.Check(clk.Now()); err != nil {
		t.Fatalf("sem modo degradado não há o que expirar: %v", err)
	}
	majorityContact(&l, 1, clk.Now())
	l.Mark(1, time.Minute)
	clk.Advance(59 * time.Second)
	if err := l.Check(clk.Now()); err != nil {
		t.Fatalf("ainda válido: %v", err)
	}
	clk.Advance(time.Second) // exatamente t_c + D: vencido
	if err := l.Check(clk.Now()); err != ErrLeaseExpired {
		t.Fatalf("esperado ErrLeaseExpired em t_c + D, veio %v", err)
	}
}

// Proposta tardia com t_c antigo: o DEGRADE só marca o modo; o lease já nasce vencido.
func TestLateDegradeWithOldContactIsExpired(t *testing.T) {
	clk := newFakeClock()
	var l Lease
	majorityContact(&l, 1, clk.Now()) // último contato com a Azure
	clk.Advance(3 * time.Hour)        // a primeira escrita só chega horas depois
	l.Mark(1, time.Minute)            // DEGRADE commitado agora
	if l.Check(clk.Now()) != ErrLeaseExpired {
		t.Fatal("o prazo conta desde t_c, não desde a proposta do DEGRADE")
	}
	majorityContact(&l, 1, clk.Now()) // a Azure volta a responder
	if err := l.Check(clk.Now()); err != nil {
		t.Fatalf("contato novo reabre a janela: %v", err)
	}
}

// t_c é o instante em que a MAIORIA confirmou: com 1 de 3 não há t_c; com 2, vale o 2º mais recente.
func TestContactNeedsMajority(t *testing.T) {
	clk := newFakeClock()
	var l Lease
	l.SetTerm(1)
	l.Mark(1, time.Minute)
	l.ObserveContact(1, "az1", clk.Now())
	if l.Check(clk.Now()) != ErrLeaseExpired {
		t.Fatal("um learner só não é maioria")
	}
	old := clk.Now()
	clk.Advance(50 * time.Second)
	l.ObserveContact(1, "az2", old)       // o 2º learner confirmou algo antigo
	l.ObserveContact(1, "az3", clk.Now()) // o 3º, algo recente
	l.ObserveContact(1, "az1", clk.Now()) // o 1º avançou
	// 2º mais recente entre {recente, recente, antigo} é recente.
	clk.Advance(30 * time.Second)
	if err := l.Check(clk.Now()); err != nil {
		t.Fatalf("t_c recente da maioria: %v", err)
	}
	// contatos nunca recuam
	l.ObserveContact(1, "az1", old)
	l.ObserveContact(1, "az3", old)
	if err := l.Check(clk.Now()); err != nil {
		t.Fatalf("contato antigo não pode recuar t_c: %v", err)
	}
}

// Líder nova (termo novo) ou nó reiniciado herda o DEGRADE sem t_c: vencido até haver
// contato no termo novo ou RENEW.
func TestNewLeaderWithoutContactIsExpired(t *testing.T) {
	clk := newFakeClock()
	var l Lease
	majorityContact(&l, 1, clk.Now())
	l.Mark(1, time.Minute)
	if l.Check(clk.Now()) != nil {
		t.Fatal("líder antiga com contato fresco está válida")
	}
	l.SetTerm(2) // eleição: contatos do termo antigo somem
	if l.Check(clk.Now()) != ErrLeaseExpired {
		t.Fatal("líder nova não herda t_c")
	}
	l.ObserveContact(1, "az1", clk.Now()) // resposta atrasada do termo antigo
	l.ObserveContact(1, "az2", clk.Now())
	if l.Check(clk.Now()) != ErrLeaseExpired {
		t.Fatal("contato de termo antigo não conta")
	}
	majorityContact(&l, 2, clk.Now())
	if l.Check(clk.Now()) != nil {
		t.Fatal("contato com a maioria no termo novo reabre")
	}

	// Reinício: Inherit marca o modo e zera contatos.
	var r Lease
	majorityContact(&r, 1, clk.Now())
	r.Inherit(1, time.Minute)
	if r.Check(clk.Now()) != ErrLeaseExpired || !r.Active() {
		t.Fatal("nó reiniciado em degradado começa vencido")
	}
}

func TestLeaseClockJumpBackwardCountsAsExpired(t *testing.T) {
	clk := newFakeClock()
	var l Lease
	majorityContact(&l, 1, clk.Now())
	l.Mark(1, time.Minute)
	clk.Advance(-time.Second)
	if l.Check(clk.Now()) != ErrLeaseExpired {
		t.Fatal("salto para trás deve contar como expirado")
	}
}

func TestLeaseRenewAndClear(t *testing.T) {
	clk := newFakeClock()
	var l Lease
	if l.Renew(1, clk.Now(), time.Minute) {
		t.Fatal("sem modo degradado não prorroga")
	}
	l.Inherit(1, time.Minute) // sem contato: vencido
	if l.Check(clk.Now()) != ErrLeaseExpired {
		t.Fatal("devia estar vencido")
	}
	if !l.Renew(1, clk.Now(), 10*time.Minute) || l.Check(clk.Now()) != nil {
		t.Fatal("RENEW humano libera mesmo sem contato com a Azure")
	}
	clk.Advance(9 * time.Minute)
	if l.Check(clk.Now()) != nil {
		t.Fatal("RENEW ainda vale")
	}
	clk.Advance(2 * time.Minute)
	if l.Check(clk.Now()) != ErrLeaseExpired {
		t.Fatal("RENEW tem prazo")
	}
	if l.Renew(2, clk.Now(), time.Minute) {
		t.Fatal("RENEW de outra época deve ser recusado")
	}
	l.SetTerm(5) // líder nova não herda o RENEW
	if l.Check(clk.Now()) != ErrLeaseExpired {
		t.Fatal("sem RENEW herdado")
	}
	l.Renew(1, clk.Now(), time.Minute)
	l.Clear()
	clk.Advance(time.Hour)
	if l.Active() || l.Check(clk.Now()) != nil {
		t.Fatal("RESTORE encerra o modo")
	}
}

func TestParseDegradedPolicy(t *testing.T) {
	for _, s := range []string{"accept", "reject"} {
		if p, err := ParseDegradedPolicy(s); err != nil || string(p) != s {
			t.Errorf("%s: %v %v", s, p, err)
		}
	}
	for _, s := range []string{"", "Accept", "yes"} {
		if _, err := ParseDegradedPolicy(s); err == nil {
			t.Errorf("%q devia falhar", s)
		}
	}
}

// Check concorrente com Grant/Renew/Clear: sem corrida de dados.
func TestLeaseConcurrentUse(t *testing.T) {
	clk := newFakeClock()
	var l Lease
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = l.Check(clk.Now())
				_ = l.Active()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				l.SetTerm(Term(j))
				l.ObserveContact(Term(j), "az1", clk.Now())
				l.ObserveContact(Term(j), "az2", clk.Now())
				l.Mark(uint64(j), time.Second)
				l.Inherit(uint64(j), time.Second)
				l.Renew(uint64(j), clk.Now(), time.Second)
				clk.Advance(time.Millisecond)
				l.Clear()
			}
		}()
	}
	wg.Wait()
}

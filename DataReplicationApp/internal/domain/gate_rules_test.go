package domain

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Testes de tabela das regras G-01 a G-09 (O-02), com learners simulados.
func TestGateRules(t *testing.T) {
	type step func(g *Gate)
	learner := func(term Term, idx Index, name string) step {
		return func(g *Gate) { g.SetAzureDurable(term, idx, name) }
	}
	commit := func(i Index) step { return func(g *Gate) { g.SetRaftCommit(i) } }
	term := func(tm Term) step { return func(g *Gate) { g.SetTerm(tm) } }
	received := func(i Index) step { return func(g *Gate) { g.SetReceived(i) } }
	follower := func(on bool) step { return func(g *Gate) { g.SetFollower(on) } }
	azureOff := func(on bool) step { return func(g *Gate) { g.SetAzureOff(on) } }

	cases := []struct {
		name      string
		steps     []step
		wantIndex Index
		wantReady bool
	}{
		{"G-01 maioria 2 de 3", []step{term(1), commit(20), learner(1, 5, "a"), learner(1, 9, "b"), learner(1, 7, "c")}, 7, true},
		{"G-01 commit limita a maioria", []step{term(1), commit(6), learner(1, 9, "a"), learner(1, 8, "b")}, 6, true},
		{"G-01 um learner só não anda", []step{term(1), commit(20), learner(1, 9, "a")}, 0, false},
		{"G-03 termo antigo ignorado", []step{term(2), commit(20), learner(1, 9, "a"), learner(1, 9, "b")}, 0, false},
		{"G-03 troca de termo zera o progresso e não recua", []step{term(1), commit(20), learner(1, 9, "a"), learner(1, 9, "b"), term(2), learner(2, 3, "a")}, 9, false},
		{"G-02 learner que recua não muda o gate", []step{term(1), commit(20), learner(1, 9, "a"), learner(1, 9, "b"), learner(1, 4, "b")}, 9, true},
		{"G-06 follower segue o recebido", []step{follower(true), commit(10), received(6)}, 6, false},
		{"G-06 commit local limita o recebido", []step{follower(true), commit(10), received(12)}, 10, false},
		{"G-06 recebido menor não recua", []step{follower(true), commit(10), received(8), received(5)}, 8, false},
		{"G-06 líder vira follower sem recuar", []step{azureOff(true), commit(8), follower(true), received(5)}, 8, true},
		{"G-06 follower vira líder sem Azure", []step{azureOff(true), follower(true), commit(12), received(4), follower(false)}, 12, true},
		{"G-07 pronto só com 2 learners", []step{term(1), learner(1, 1, "a"), learner(1, 1, "b")}, 0, true},
		{"G-08 sem Azure gate = commit e pronto", []step{azureOff(true), commit(15)}, 15, true},
		{"valor zero mantém o produto: sem learners não anda", []step{commit(15)}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := &Gate{}
			for _, s := range c.steps {
				s(g)
			}
			if got := g.Index(); got != c.wantIndex {
				t.Fatalf("gate = %d, quer %d", got, c.wantIndex)
			}
			if got := g.Ready(); got != c.wantReady {
				t.Fatalf("pronto = %v, quer %v", got, c.wantReady)
			}
			if st := g.Snapshot(); st.Index != c.wantIndex || st.Ready != c.wantReady {
				t.Fatalf("retrato incoerente: %+v", st)
			}
		})
	}
}

// G-09: quem espera e desiste não deixa registro; Changed acorda a cada avanço.
func TestGateChangedAndWaitCleanup(t *testing.T) {
	g := &Gate{}
	g.SetAzureOff(true)
	ch := g.Changed()
	g.SetRaftCommit(3)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("Changed não acordou no avanço")
	}
	ch = g.Changed()
	g.SetRaftCommit(3) // sem avanço
	select {
	case <-ch:
		t.Fatal("Changed acordou sem avanço")
	default:
	}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			_ = g.WaitFor(ctx, 100)
		}()
	}
	wg.Wait()
	if n := g.waiting(); n != 0 {
		t.Fatalf("restaram %d waiters", n)
	}
}

// Avanços concorrentes de papel, recebido e commit nunca fazem o gate recuar.
func TestGateConcurrentRoleSwitchIsMonotonic(t *testing.T) {
	g := &Gate{}
	g.SetAzureOff(true)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := Index(1); i <= 2000; i++ {
			g.SetRaftCommit(i)
		}
	}()
	go func() {
		defer wg.Done()
		for i := Index(1); i <= 2000; i++ {
			g.SetReceived(i)
			g.SetFollower(i%2 == 0)
		}
	}()
	go func() {
		defer wg.Done()
		var last Index
		for {
			select {
			case <-stop:
				return
			default:
			}
			cur := g.Index()
			if cur < last {
				t.Errorf("gate recuou de %d para %d", last, cur)
				return
			}
			last = cur
		}
	}()
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

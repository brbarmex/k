package domain

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Testes de tabela das regras do gate (O-02).
func TestGateRules(t *testing.T) {
	type step func(g *Gate)
	commit := func(i Index) step { return func(g *Gate) { g.SetRaftCommit(i) } }
	received := func(i Index) step { return func(g *Gate) { g.SetReceived(i) } }
	follower := func(on bool) step { return func(g *Gate) { g.SetFollower(on) } }

	cases := []struct {
		name      string
		steps     []step
		wantIndex Index
	}{
		{"líder: gate = commit", []step{commit(15)}, 15},
		{"G-06 follower segue o recebido", []step{follower(true), commit(10), received(6)}, 6},
		{"G-06 commit local limita o recebido", []step{follower(true), commit(10), received(12)}, 10},
		{"G-06 recebido menor não recua", []step{follower(true), commit(10), received(8), received(5)}, 8},
		{"G-06 líder vira follower sem recuar", []step{commit(8), follower(true), received(5)}, 8},
		{"G-06 follower vira líder", []step{follower(true), commit(12), received(4), follower(false)}, 12},
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
			if st := g.Snapshot(); st.Index != c.wantIndex {
				t.Fatalf("retrato incoerente: %+v", st)
			}
		})
	}
}

// G-09: quem espera e desiste não deixa registro; Changed acorda a cada avanço.
func TestGateChangedAndWaitCleanup(t *testing.T) {
	g := &Gate{}
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

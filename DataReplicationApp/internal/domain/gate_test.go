package domain

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// O bug original: WaitFor deixava o waiter para trás quando o contexto expirava.
func TestWaitForRemovesWaiterOnTimeout(t *testing.T) {
	g := &Gate{}
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, c := context.WithTimeout(context.Background(), 5*time.Millisecond)
			defer c()
			if g.WaitFor(ctx, Index(1000+i)) == nil {
				t.Error("não devia liberar")
			}
		}()
	}
	wg.Wait()
	if n := g.waiting(); n != 0 {
		t.Fatalf("vazamento: %d waiters depois dos timeouts", n)
	}
}

func TestWaitForCancelBeforeAndRelease(t *testing.T) {
	g := &Gate{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.WaitFor(ctx, 5); err == nil {
		t.Fatal("contexto cancelado devia falhar")
	}
	if g.waiting() != 0 {
		t.Fatal("waiter ficou para trás")
	}
	g.SetRaftCommit(5)
	if err := g.WaitFor(ctx, 5); err != nil { // já liberado: nem olha o contexto
		t.Fatalf("índice já liberado devia passar: %v", err)
	}
}

// Muitos esperantes, avanço concorrente do commit: todos liberam, o valor observado nunca
// recua e não sobra waiter.
func TestGateConcurrentAdvanceIsMonotonicAndReleasesAll(t *testing.T) {
	g := &Gate{}
	const n = 300
	var released atomic.Int64
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(idx Index) {
			defer wg.Done()
			ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			if err := g.WaitFor(ctx, idx); err != nil {
				t.Errorf("idx %d: %v", idx, err)
				return
			}
			if g.Index() < idx {
				t.Errorf("liberou %d com gate em %d", idx, g.Index())
			}
			released.Add(1)
		}(Index(i))
	}

	stop := make(chan struct{})
	var mon sync.WaitGroup
	mon.Add(1)
	go func() { // observador: o gate nunca pode recuar
		defer mon.Done()
		var last Index
		for {
			select {
			case <-stop:
				return
			default:
				cur := g.Index()
				if cur < last {
					t.Errorf("gate recuou de %d para %d", last, cur)
					return
				}
				last = cur
			}
		}
	}()

	var feeders sync.WaitGroup
	feeders.Add(1)
	go func() {
		defer feeders.Done()
		for i := 1; i <= n; i++ {
			g.SetRaftCommit(Index(i))
		}
	}()
	feeders.Wait()
	wg.Wait()
	close(stop)
	mon.Wait()
	if released.Load() != n || g.Index() != n || g.waiting() != 0 {
		t.Fatalf("liberados=%d idx=%d waiting=%d", released.Load(), g.Index(), g.waiting())
	}
}

// Corrida entre a liberação e a expiração do contexto: nunca vaza, nunca deadlock.
func TestWaitForRaceReleaseVsTimeout(t *testing.T) {
	for round := 0; round < 100; round++ {
		g := &Gate{}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, c := context.WithTimeout(context.Background(), time.Duration(round%5)*time.Millisecond)
				defer c()
				_ = g.WaitFor(ctx, 1)
			}()
		}
		time.Sleep(time.Duration(round%3) * time.Millisecond)
		g.SetRaftCommit(1)
		wg.Wait()
		if g.waiting() != 0 {
			t.Fatalf("rodada %d: %d waiters vazados", round, g.waiting())
		}
	}
}

// waiting devolve quantos waiters estão registrados (testes de vazamento).
func (g *Gate) waiting() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.waiters)
}

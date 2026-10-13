package domain

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newGate(term Term) *Gate {
	g := &Gate{}
	g.SetTerm(term)
	return g
}

func TestGate(t *testing.T) {
	g := newGate(1)
	g.SetRaftCommit(10)
	g.SetAzureDurable(1, 10, "az1")
	g.SetAzureDurable(1, 7, "az2")
	g.SetAzureDurable(1, 3, "az3")
	if g.Index() != 7 { // 2º maior entre {10, 7, 3}
		t.Fatalf("esperado 7, veio %d", g.Index())
	}
	g.SetAzureDurable(1, 5, "az2") // o learner não recua
	if g.Index() != 7 {
		t.Fatal("gate recuou")
	}
	ctx, c := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer c()
	if g.WaitFor(ctx, 9) == nil {
		t.Fatal("não devia liberar")
	}
	g.SetDegraded(true)
	if g.WaitFor(context.Background(), 10) != nil {
		t.Fatal("degradado devia liberar até o commit Raft")
	}
}

// Com menos de 2 learners confirmados a Azure não forma quórum.
func TestGateNeedsTwoLearners(t *testing.T) {
	g := newGate(1)
	g.SetRaftCommit(10)
	g.SetAzureDurable(1, 10, "az1")
	if g.Index() != 0 || g.Ready() {
		t.Fatalf("um learner só não basta: idx=%d ready=%v", g.Index(), g.Ready())
	}
	g.SetAzureDurable(1, 8, "az2")
	if g.Index() != 8 || !g.Ready() {
		t.Fatalf("esperado 8 e pronto, veio %d %v", g.Index(), g.Ready())
	}
}

// ADR-046: só o termo atual conta. Progresso de termo antigo ou futuro é ignorado.
func TestGateAdoptsOnlyCurrentTerm(t *testing.T) {
	g := newGate(5)
	g.SetRaftCommit(100)
	for _, term := range []Term{4, 6, 0} {
		if g.SetAzureDurable(term, 100, "az1") || g.SetAzureDurable(term, 100, "az2") {
			t.Fatalf("termo %d não devia ser adotado no termo 5", term)
		}
	}
	if g.Index() != 0 {
		t.Fatalf("nada devia ter sido liberado, veio %d", g.Index())
	}
	if !g.SetAzureDurable(5, 100, "az1") || !g.SetAzureDurable(5, 90, "az2") {
		t.Fatal("termo atual devia ser adotado")
	}
	if g.Index() != 90 {
		t.Fatalf("esperado 90, veio %d", g.Index())
	}
}

// Líder nova: o progresso herdado some, o gate liberado não recua, e só a confirmação
// no termo novo reconstrói o quórum.
func TestGateNewTermRebuildsWithoutRecuar(t *testing.T) {
	g := newGate(1)
	g.SetRaftCommit(50)
	g.SetAzureDurable(1, 50, "az1")
	g.SetAzureDurable(1, 50, "az2")
	if g.Index() != 50 {
		t.Fatalf("esperado 50, veio %d", g.Index())
	}
	g.SetTerm(2)
	if g.Index() != 50 {
		t.Fatalf("o gate não pode recuar na troca de líder: %d", g.Index())
	}
	if g.Ready() {
		t.Fatal("líder nova não está pronta antes de confirmar no termo novo")
	}
	g.SetRaftCommit(60)
	g.SetAzureDurable(1, 60, "az1") // resposta atrasada do termo antigo
	g.SetAzureDurable(1, 60, "az2")
	if g.Index() != 50 || g.Ready() {
		t.Fatalf("progresso do termo antigo foi adotado: idx=%d", g.Index())
	}
	g.SetAzureDurable(2, 55, "az1")
	g.SetAzureDurable(2, 58, "az2")
	if g.Index() != 55 || !g.Ready() {
		t.Fatalf("esperado 55 e pronto, veio %d %v", g.Index(), g.Ready())
	}
	g.SetTerm(1) // termo menor é ignorado
	if g.Term() != 2 {
		t.Fatalf("termo recuou: %d", g.Term())
	}
}

func TestGateRejectsFourthLearnerUntilForget(t *testing.T) {
	g := newGate(1)
	for _, id := range []string{"a", "b", "c"} {
		g.SetAzureDurable(1, 1, id)
	}
	if g.SetAzureDurable(1, 9, "d") {
		t.Fatal("quarto learner devia ser recusado")
	}
	g.ForgetLearner("c") // disco trocado, identidade nova (ADR-034)
	if !g.SetAzureDurable(1, 9, "d") {
		t.Fatal("depois do Forget a identidade nova entra")
	}
}

// O bug original: WaitFor deixava o waiter para trás quando o contexto expirava.
func TestWaitForRemovesWaiterOnTimeout(t *testing.T) {
	g := newGate(1)
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
	if n := g.Waiting(); n != 0 {
		t.Fatalf("vazamento: %d waiters depois dos timeouts", n)
	}
}

func TestWaitForCancelBeforeAndRelease(t *testing.T) {
	g := newGate(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.WaitFor(ctx, 5); err == nil {
		t.Fatal("contexto cancelado devia falhar")
	}
	if g.Waiting() != 0 {
		t.Fatal("waiter ficou para trás")
	}
	g.SetRaftCommit(5)
	g.SetAzureDurable(1, 5, "a")
	g.SetAzureDurable(1, 5, "b")
	if err := g.WaitFor(ctx, 5); err != nil { // já liberado: nem olha o contexto
		t.Fatalf("índice já liberado devia passar: %v", err)
	}
}

// Muitos esperantes, avanço concorrente vindo de vários learners: todos liberam,
// o valor observado nunca recua e não sobra waiter.
func TestGateConcurrentAdvanceIsMonotonicAndReleasesAll(t *testing.T) {
	g := newGate(1)
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
	for _, id := range []string{"az1", "az2", "az3"} {
		feeders.Add(1)
		go func() {
			defer feeders.Done()
			for i := 1; i <= n; i++ {
				g.SetAzureDurable(1, Index(i), id)
			}
		}()
	}
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
	if released.Load() != n || g.Index() != n || g.Waiting() != 0 {
		t.Fatalf("liberados=%d idx=%d waiting=%d", released.Load(), g.Index(), g.Waiting())
	}
}

// Corrida entre a liberação e a expiração do contexto: nunca vaza, nunca deadlock.
func TestWaitForRaceReleaseVsTimeout(t *testing.T) {
	for round := 0; round < 100; round++ {
		g := newGate(1)
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
		g.SetAzureDurable(1, 1, "a")
		g.SetAzureDurable(1, 1, "b")
		wg.Wait()
		if g.Waiting() != 0 {
			t.Fatalf("rodada %d: %d waiters vazados", round, g.Waiting())
		}
	}
}

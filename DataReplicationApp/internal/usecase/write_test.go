package usecase

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

type rig struct {
	svc  *WriteService
	g    *domain.Gate
	cons *fakeConsensus
	fwd  *fakeForwarder
}

func newRig(applied fakeApplied) *rig {
	g := &domain.Gate{}
	cons := newFakeConsensus()
	fwd := &fakeForwarder{}
	svc := &WriteService{Consensus: cons, Forward: fwd, Gate: g, Applied: applied}
	return &rig{svc: svc, g: g, cons: cons, fwd: fwd}
}

func timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// O ACK não sai com o gate liberado se o apply local ainda não terminou.
func TestAckWaitsForGateAndApply(t *testing.T) {
	applied := fakeApplied{done: make(chan struct{})}
	r := newRig(applied)
	releaseGate(r.g, 100)

	ctx, cancel := timeout(30 * time.Millisecond)
	defer cancel()
	if _, err := r.svc.Write(ctx, port.WriteRequest{}); !errors.Is(err, ErrPending) {
		t.Fatalf("esperado ErrPending sem apply, veio %v", err)
	}

	close(applied.done)
	res, err := r.svc.Write(context.Background(), port.WriteRequest{})
	if err != nil || res.Index != 6 {
		t.Fatalf("esperado ACK no índice 6, veio %v %v", res, err)
	}
}

// Sem o gate (sem commit), o ACK também espera, mesmo com o apply pronto.
func TestAckWaitsForGate(t *testing.T) {
	r := newRig(appliedOK())

	ctx, cancel := timeout(30 * time.Millisecond)
	defer cancel()
	if _, err := r.svc.Write(ctx, port.WriteRequest{}); !errors.Is(err, ErrPending) {
		t.Fatalf("esperado ErrPending sem gate, veio %v", err)
	}
}

// ADR-039: sem líder conhecida espera até WAIT_LEADER, responde ErrNoLeader (503) e
// NÃO segue para o Propose.
func TestNoLeaderWaitsThenFailsWithoutPropose(t *testing.T) {
	r := newRig(appliedOK())
	r.cons.setLeader(port.LeaderInfo{}, false)
	r.svc.WaitLeader = 80 * time.Millisecond

	start := time.Now()
	_, err := r.svc.Write(context.Background(), port.WriteRequest{})
	el := time.Since(start)
	if !errors.Is(err, ErrNoLeader) {
		t.Fatalf("esperado ErrNoLeader, veio %v", err)
	}
	if el < 70*time.Millisecond || el > 2*time.Second {
		t.Fatalf("devia esperar ~WAIT_LEADER, esperou %v", el)
	}
	if n := r.cons.total(); n != 0 {
		t.Fatalf("sem líder não pode haver Propose, houve %d", n)
	}
}

func TestLeaderAppearsDuringWait(t *testing.T) {
	r := newRig(appliedOK())
	releaseGate(r.g, 100)
	r.cons.setLeader(port.LeaderInfo{}, false)
	r.svc.WaitLeader = 2 * time.Second
	go func() {
		time.Sleep(30 * time.Millisecond)
		r.cons.setLeader(port.LeaderInfo{NodeID: "n1", Local: true}, true)
	}()
	if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); err != nil {
		t.Fatalf("a eleição terminou dentro da espera: %v", err)
	}
	if r.cons.count(domain.KindWrite) != 1 {
		t.Fatal("devia ter proposto uma vez")
	}
}

func TestNoLeaderStopsOnContext(t *testing.T) {
	r := newRig(appliedOK())
	r.cons.setLeader(port.LeaderInfo{}, false)
	r.svc.WaitLeader = 10 * time.Second
	ctx, cancel := timeout(30 * time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := r.svc.Write(ctx, port.WriteRequest{}); !errors.Is(err, ErrNoLeader) || time.Since(start) > time.Second {
		t.Fatalf("a espera deve respeitar o prazo da requisição: %v", err)
	}
}

// Teto de requisições esperando líder: o excesso falha na hora.
func TestMaxWaitingLeader(t *testing.T) {
	r := newRig(appliedOK())
	r.cons.setLeader(port.LeaderInfo{}, false)
	r.svc.WaitLeader = 300 * time.Millisecond
	r.svc.MaxWaitingLeader = 2

	var slow atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); !errors.Is(err, ErrNoLeader) {
				t.Errorf("esperado ErrNoLeader, veio %v", err)
			}
			if time.Since(start) >= 250*time.Millisecond {
				slow.Add(1)
			}
		}()
	}
	wg.Wait()
	if slow.Load() > 2 {
		t.Fatalf("no máximo 2 esperam a eleição inteira, esperaram %d", slow.Load())
	}
	if r.cons.total() != 0 {
		t.Fatal("ninguém propõe sem líder")
	}
}

// ADR-039, item 1: requisição já encaminhada nunca é reencaminhada.
func TestForwardedIsNeverReforwarded(t *testing.T) {
	r := newRig(appliedOK())
	r.cons.setLeader(port.LeaderInfo{NodeID: "n2", Local: false}, true)

	if _, err := r.svc.Write(context.Background(), port.WriteRequest{Forwarded: true}); !errors.Is(err, ErrNoLeader) {
		t.Fatalf("esperado ErrNoLeader, veio %v", err)
	}
	if len(r.fwd.calls) != 0 {
		t.Fatal("reencaminhou uma requisição já encaminhada")
	}
	res, err := r.svc.Write(context.Background(), port.WriteRequest{})
	if err != nil || res.Index != 99 || len(r.fwd.calls) != 1 || !r.fwd.calls[0].Forwarded {
		t.Fatalf("o follower encaminha uma vez, marcando Forwarded: %v %v %+v", res, err, r.fwd.calls)
	}
	if r.cons.total() != 0 {
		t.Fatal("follower não propõe")
	}
}

// Caminho feliz sob concorrência: o gate é alimentado em paralelo e todas as escritas ACKam.
func TestConcurrentWritesHappyPath(t *testing.T) {
	r := newRig(appliedOK())
	const n = 100
	stop := make(chan struct{})
	go func() {
		for i := domain.Index(5); ; i += 5 {
			select {
			case <-stop:
				return
			default:
				releaseGate(r.g, i)
				time.Sleep(time.Millisecond)
			}
		}
	}()
	defer close(stop)

	seen := sync.Map{}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := timeout(5 * time.Second)
			defer cancel()
			res, err := r.svc.Write(ctx, port.WriteRequest{})
			if err != nil {
				t.Errorf("write: %v", err)
				return
			}
			if _, dup := seen.LoadOrStore(res.Index, true); dup {
				t.Errorf("índice repetido %d", res.Index)
			}
		}()
	}
	wg.Wait()
}

// Buffer não persistido cheio: o erro chega ao cliente como está, sem esperar gate nem
// apply, e nada foi proposto (ADR-056).
func TestBackpressureFromProposeIsReturnedAsIs(t *testing.T) {
	r := newRig(fakeApplied{})
	r.cons.mu.Lock()
	r.cons.proposeErr = ErrBackpressure
	r.cons.mu.Unlock()
	ctx, cancel := timeout(time.Second)
	defer cancel()
	_, err := r.svc.Write(ctx, port.WriteRequest{})
	if !errors.Is(err, ErrBackpressure) {
		t.Fatalf("esperado ErrBackpressure, veio %v", err)
	}
	if n := r.cons.count(domain.KindWrite); n != 0 {
		t.Fatalf("%d escritas propostas", n)
	}
}

// ADR-060: com o apply local mais de MaxApplyLag atrás do gate, a líder recusa a escrita
// ANTES de propor (nada entra no log), com a causa atraso_apply.
func TestAdmissionRefusesWhenApplyLags(t *testing.T) {
	m := &recMetrics{}
	r := newRig(fakeApplied{done: make(chan struct{}), applied: 10})
	r.svc.MaxApplyLag, r.svc.Metrics = 100, m
	r.g.SetRaftCommit(500) // gate 500, aplicado 10: atraso 490
	_, err := r.svc.Write(context.Background(), port.WriteRequest{})
	var bp *BackpressureError
	if !errors.As(err, &bp) || bp.Cause != CauseApplyLag || !errors.Is(err, ErrBackpressure) {
		t.Fatalf("esperado contrapressão atraso_apply, veio %v", err)
	}
	if r.cons.total() != 0 {
		t.Fatal("a escrita recusada não pode gastar índice do log")
	}
	if m.n("throttled:atraso_apply") != 1 {
		t.Fatal("a causa não foi registrada")
	}
}

// No limite (atraso igual a MaxApplyLag) a escrita entra; a admissão mede contra o gate,
// não contra o commit (na follower, entradas esperando o gate da líder não são atraso do apply).
func TestAdmissionAllowsUpToLimitAndIgnoresGateWait(t *testing.T) {
	r := newRig(appliedOK())
	r.svc.Applied = fakeApplied{done: appliedOK().done, applied: 400}
	r.svc.MaxApplyLag = 100
	r.g.SetRaftCommit(500)
	if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); err != nil {
		t.Fatalf("atraso no limite devia passar: %v", err)
	}

	r2 := newRig(fakeApplied{done: make(chan struct{}), applied: 0})
	r2.svc.MaxApplyLag = 100
	r2.g.SetFollower(true)
	r2.g.SetRaftCommit(10_000) // commit muito à frente, mas a líder não publicou o gate: gate 0
	ctx, cancel := timeout(30 * time.Millisecond)
	defer cancel()
	if _, err := r2.svc.Write(ctx, port.WriteRequest{}); errors.Is(err, ErrBackpressure) {
		t.Fatal("espera do gate não é atraso do apply")
	}
}

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

const leaseD = time.Minute

type rig struct {
	svc  *WriteService
	deg  *DegradeService
	g    *domain.Gate
	cons *fakeConsensus
	clk  *fakeClock
	fwd  *fakeForwarder
}

func newRig(applied fakeApplied, policy domain.DegradedPolicy) *rig {
	g := &domain.Gate{}
	g.SetTerm(1)
	cons := newFakeConsensus()
	clk := newFakeClock()
	deg := &DegradeService{Policy: policy, LeaseDuration: leaseD, Consensus: cons, Gate: g, Lease: &domain.Lease{}, Clock: clk.Now}
	fwd := &fakeForwarder{}
	if policy == domain.PolicyAccept {
		deg.Lease.SetTerm(1)
		contact(deg.Lease, 1, clk.Now()) // a Azure respondeu agora: t_c = hoje
	}
	svc := &WriteService{Consensus: cons, Forward: fwd, Gate: g, Applied: applied, Degrade: deg}
	return &rig{svc: svc, deg: deg, g: g, cons: cons, clk: clk, fwd: fwd}
}

// contact simula heartbeats confirmados por 2 de 3 learners da Azure, enviados em at.
func contact(l *domain.Lease, term domain.Term, at time.Time) {
	l.ObserveContact(term, "az1", at)
	l.ObserveContact(term, "az2", at)
}

func timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// O ACK não sai com o gate liberado se o apply local ainda não terminou.
func TestAckWaitsForGateAndApply(t *testing.T) {
	applied := fakeApplied{done: make(chan struct{})}
	r := newRig(applied, domain.PolicyReject)
	releaseGate(r.g, 1, 100)

	ctx, cancel := timeout(30 * time.Millisecond)
	defer cancel()
	if _, err := r.svc.Write(ctx, port.WriteRequest{}); !errors.Is(err, ErrPending) {
		t.Fatalf("esperado ErrPending sem apply, veio %v", err)
	}

	close(applied.done)
	res, err := r.svc.Write(context.Background(), port.WriteRequest{})
	if err != nil || res.Index != 6 || res.Degraded {
		t.Fatalf("esperado ACK no índice 6 sem degradado, veio %v %v", res, err)
	}
}

// Sem o gate (Azure não confirmou), o ACK também espera, mesmo com o apply pronto.
func TestAckWaitsForAzureGate(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyReject)
	r.g.SetRaftCommit(5)

	ctx, cancel := timeout(30 * time.Millisecond)
	defer cancel()
	if _, err := r.svc.Write(ctx, port.WriteRequest{}); !errors.Is(err, ErrPending) {
		t.Fatalf("esperado ErrPending sem gate, veio %v", err)
	}
}

// ADR-039: sem líder conhecida espera até WAIT_LEADER, responde ErrNoLeader (503) e
// NÃO segue para o Propose.
func TestNoLeaderWaitsThenFailsWithoutPropose(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyReject)
	r.cons.setLeader(port.LeaderInfo{}, false)
	r.svc.WaitLeader = 80 * time.Millisecond
	r.svc.LeaderPoll = 5 * time.Millisecond

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
	r := newRig(appliedOK(), domain.PolicyReject)
	releaseGate(r.g, 1, 100)
	r.cons.setLeader(port.LeaderInfo{}, false)
	r.svc.WaitLeader = 2 * time.Second
	r.svc.LeaderPoll = 5 * time.Millisecond
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
	r := newRig(appliedOK(), domain.PolicyReject)
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
	r := newRig(appliedOK(), domain.PolicyReject)
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
	r := newRig(appliedOK(), domain.PolicyReject)
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

// ADR-044: lease vencido antes do Propose recusa sem gastar índice.
func TestLeaseExpiredBeforePropose(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyAccept)
	r.deg.Lease.Mark(1, leaseD)
	r.g.SetDegraded(true)
	r.g.SetRaftCommit(100)
	r.clk.Advance(leaseD + time.Second) // sem contato novo com a Azure

	if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("esperado ErrLeaseExpired, veio %v", err)
	}
	if r.cons.total() != 0 {
		t.Fatal("lease vencido não pode gastar índice do log")
	}
}

// ADR-044: pausa de processo DURANTE a espera (o relógio anda enquanto o apply demora).
// A conferência a cada ACK pega o lease vencido, mesmo com gate e apply liberados.
func TestLeaseCheckedAtEveryAck(t *testing.T) {
	var clk *fakeClock
	applied := appliedOK()
	applied.onWait = func() { clk.Advance(leaseD + time.Second) } // pausa de processo
	r := newRig(applied, domain.PolicyAccept)
	clk = r.clk
	r.deg.Lease.Mark(1, leaseD)
	r.g.SetDegraded(true)
	r.g.SetRaftCommit(100)

	_, err := r.svc.Write(context.Background(), port.WriteRequest{})
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("esperado ErrLeaseExpired no ACK, veio %v", err)
	}
	if r.cons.count(domain.KindWrite) != 1 {
		t.Fatal("a escrita estava commitada; o 503 é resultado desconhecido")
	}
	// Prorrogação humana reabre, mesmo sem contato com a Azure.
	r.deg.Lease.Renew(1, clk.Now(), leaseD)
	applied.onWait = nil
	r.svc.Applied = appliedOK()
	if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); err != nil {
		t.Fatalf("depois do RENEW devia aceitar: %v", err)
	}
}

// accept: sem a Azure por DEGRADE_AFTER, propõe DEGRADE com lease e ACKa com o header.
func TestAcceptEntersDegradedAfterDegradeAfter(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyAccept)
	r.svc.DegradeAfter = 30 * time.Millisecond
	r.g.SetRaftCommit(100) // Raft ok, Azure nunca confirma

	res, err := r.svc.Write(context.Background(), port.WriteRequest{})
	if err != nil || !res.Degraded {
		t.Fatalf("esperado ACK degradado, veio %+v %v", res, err)
	}
	es := r.cons.entries()
	if len(es) != 2 || es[0].Kind != domain.KindWrite || es[1].Kind != domain.KindDegrade || es[1].Epoch != 1 || es[1].Lease != leaseD {
		t.Fatalf("entradas inesperadas: %+v", es)
	}
}

// reject: NUNCA entra em degradado. Sem o gate da Azure o cliente recebe PENDING.
func TestRejectNeverDegrades(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyReject)
	r.svc.DegradeAfter = 10 * time.Millisecond
	r.g.SetRaftCommit(100)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := timeout(120 * time.Millisecond)
			defer cancel()
			if _, err := r.svc.Write(ctx, port.WriteRequest{}); !errors.Is(err, ErrPending) {
				t.Errorf("esperado ErrPending, veio %v", err)
			}
		}()
	}
	wg.Wait()
	if r.cons.count(domain.KindDegrade) != 0 || r.deg.Active() {
		t.Fatal("reject propôs DEGRADE ou ficou degradado")
	}
	if r.g.Index() != 0 {
		t.Fatalf("o gate liberou sem a Azure: %d", r.g.Index())
	}
}

// Muitas escritas ao mesmo tempo, Azure fora: um único DEGRADE, todas ACKam degradadas.
func TestConcurrentWritesEnterDegradedOnce(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyAccept)
	r.svc.DegradeAfter = 20 * time.Millisecond
	r.g.SetRaftCommit(1000)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := r.svc.Write(context.Background(), port.WriteRequest{})
			if err != nil || !res.Degraded {
				t.Errorf("esperado ACK degradado: %+v %v", res, err)
			}
		}()
	}
	wg.Wait()
	if n := r.cons.count(domain.KindDegrade); n != 1 {
		t.Fatalf("esperado 1 DEGRADE, houve %d", n)
	}
}

// O campo Degraded do resultado reflete o estado REAL do shard e não uma constante.
func TestDegradedFlagFollowsShardState(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyAccept)
	r.svc.DegradeAfter = 10 * time.Millisecond
	releaseGate(r.g, 1, 1000)
	res, err := r.svc.Write(context.Background(), port.WriteRequest{})
	if err != nil || res.Degraded {
		t.Fatalf("shard normal: %+v %v", res, err)
	}
	if err := r.deg.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err = r.svc.Write(context.Background(), port.WriteRequest{})
	if err != nil || !res.Degraded {
		t.Fatalf("shard degradado: %+v %v", res, err)
	}
	if err := r.deg.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, _ = r.svc.Write(context.Background(), port.WriteRequest{})
	if res.Degraded {
		t.Fatal("depois do RESTORE o header sai")
	}
}

// Caminho feliz sob concorrência: o gate é alimentado em paralelo e todas as escritas ACKam.
func TestConcurrentWritesHappyPath(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyReject)
	const n = 100
	stop := make(chan struct{})
	go func() {
		for i := domain.Index(5); ; i += 5 {
			select {
			case <-stop:
				return
			default:
				releaseGate(r.g, 1, i)
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

// Proposta tardia com t_c antigo: o DEGRADE só marca o modo e a escrita recebe LEASE_EXPIRED,
// mesmo com o DEGRADE recém-commitado.
func TestLateDegradeWithOldContactRefusesWrites(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyAccept) // t_c = agora
	r.svc.DegradeAfter = 10 * time.Millisecond
	r.g.SetRaftCommit(1000)
	r.clk.Advance(3 * leaseD) // a Azure some e a primeira escrita só chega muito depois

	if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("esperado ErrLeaseExpired, veio %v", err)
	}
	// Se o DEGRADE entra mesmo assim (outra requisição), o ACK segue recusado.
	if err := r.deg.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("lease nasce vencido: %v", err)
	}
	// Contato novo com a maioria Azure reabre a janela.
	contact(r.deg.Lease, 1, r.clk.Now())
	if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); err != nil {
		t.Fatalf("depois do contato: %v", err)
	}
}

// Líder nova que herda o DEGRADE: sem escrita até contato no termo novo ou RENEW.
func TestNewLeaderInheritingDegradeRefusesUntilContactOrRenew(t *testing.T) {
	r := newRig(appliedOK(), domain.PolicyAccept)
	r.g.SetRaftCommit(1000)
	r.deg.Adopt(domain.Entry{Kind: domain.KindDegrade, Epoch: 7, Lease: leaseD})
	r.deg.Lease.SetTerm(2)

	if _, err := r.svc.Write(context.Background(), port.WriteRequest{}); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("líder nova sem t_c: esperado ErrLeaseExpired, veio %v", err)
	}
	if err := r.deg.Renew(context.Background(), leaseD); err != nil {
		t.Fatal(err)
	}
	if res, err := r.svc.Write(context.Background(), port.WriteRequest{}); err != nil || !res.Degraded {
		t.Fatalf("RENEW libera: %+v %v", res, err)
	}
}

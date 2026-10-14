package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
)

func newDeg(policy domain.DegradedPolicy) (*DegradeService, *fakeConsensus, *domain.Gate, *fakeClock) {
	g := &domain.Gate{}
	g.SetTerm(1)
	cons := newFakeConsensus()
	clk := newFakeClock()
	d := &DegradeService{Policy: policy, LeaseDuration: leaseD, Consensus: cons, Gate: g, Lease: &domain.Lease{}, Clock: clk.Now}
	d.Lease.SetTerm(1)
	contact(d.Lease, 1, clk.Now()) // t_c = agora
	return d, cons, g, clk
}

func TestRejectRefusesEnter(t *testing.T) {
	for _, p := range []domain.DegradedPolicy{domain.PolicyReject, "", "talvez"} {
		d, cons, g, _ := newDeg(p)
		if err := d.Enter(context.Background()); !errors.Is(err, ErrDegradeRefused) {
			t.Fatalf("%q: esperado ErrDegradeRefused, veio %v", p, err)
		}
		g.SetRaftCommit(10)
		if d.Active() || cons.total() != 0 || g.Index() != 0 {
			t.Fatalf("%q: não pode ter efeito algum", p)
		}
	}
}

// A ordem do Enter: o DEGRADE só marca o modo depois do commit; o gate só deixa de contar a
// Azure depois disso. O prazo conta desde t_c, não desde a proposta nem desde o commit.
func TestEnterOrderAndLeaseCountsFromLastContact(t *testing.T) {
	d, cons, g, clk := newDeg(domain.PolicyAccept)
	g.SetRaftCommit(10)
	cons.onPropose = func(e domain.Entry) {
		if d.Active() || g.Index() != 0 {
			t.Error("modo ou gate degradado antes do commit do DEGRADE")
		}
		clk.Advance(40 * time.Second) // demora até o commit
	}
	if err := d.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !d.Active() || g.Index() != 10 {
		t.Fatalf("depois do commit: active=%v gate=%d", d.Active(), g.Index())
	}
	if err := d.CheckLease(); err != nil {
		t.Fatalf("40 s de 60 s desde t_c: %v", err)
	}
	clk.Advance(21 * time.Second) // 61 s desde t_c
	if !errors.Is(d.CheckLease(), ErrLeaseExpired) {
		t.Fatal("o lease conta desde t_c")
	}
}

// Proposta tardia com t_c antigo: expirado logo ao entrar.
func TestEnterLateWithOldContactIsExpired(t *testing.T) {
	d, _, g, clk := newDeg(domain.PolicyAccept)
	g.SetRaftCommit(10)
	clk.Advance(3 * leaseD)
	if err := d.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !d.Active() || !errors.Is(d.CheckLease(), ErrLeaseExpired) {
		t.Fatal("DEGRADE tardio nasce vencido")
	}
}

// Líder nova ou nó reiniciado: Adopt herda o DEGRADE sem t_c; contato ou RENEW libera.
func TestAdoptInheritsDegradeWithoutContact(t *testing.T) {
	d, cons, g, clk := newDeg(domain.PolicyAccept)
	d.Lease.SetTerm(2) // eleição: contatos do termo antigo somem
	d.Adopt(domain.Entry{Kind: domain.KindDegrade, Epoch: 3, Lease: leaseD})
	if !d.Active() || !errors.Is(d.CheckLease(), ErrLeaseExpired) {
		t.Fatal("herdou o modo, mas o lease começa vencido")
	}
	g.SetRaftCommit(10)
	if g.Index() != 10 {
		t.Fatal("o gate herda o modo degradado")
	}
	contact(d.Lease, 2, clk.Now())
	if err := d.CheckLease(); err != nil {
		t.Fatalf("contato com a maioria Azure no termo novo libera: %v", err)
	}
	clk.Advance(2 * leaseD)
	if !errors.Is(d.CheckLease(), ErrLeaseExpired) {
		t.Fatal("vence de novo sem contato")
	}
	if err := d.Renew(context.Background(), leaseD); err != nil || d.CheckLease() != nil {
		t.Fatalf("RENEW libera: %v", err)
	}
	if cons.count(domain.KindRenew) != 1 {
		t.Fatal("RENEW é uma entrada replicada")
	}
	d.Adopt(domain.Entry{Kind: domain.KindRestore, Epoch: 3})
	if d.Active() {
		t.Fatal("RESTORE encerra o modo")
	}
}

func TestEnterProposeFailureStaysNormal(t *testing.T) {
	d, cons, g, _ := newDeg(domain.PolicyAccept)
	g.SetRaftCommit(10)
	cons.proposeErr = errors.New("sem quórum")
	if err := d.Enter(context.Background()); err == nil {
		t.Fatal("esperava erro")
	}
	if d.Active() || g.Index() != 0 {
		t.Fatal("DEGRADE que não commitou não pode degradar")
	}
}

func TestRestoreAndRenew(t *testing.T) {
	d, cons, g, clk := newDeg(domain.PolicyAccept)
	g.SetRaftCommit(10)
	if err := d.Renew(context.Background(), leaseD); err == nil {
		t.Fatal("sem lease ativo não há o que prorrogar")
	}
	if err := d.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * leaseD)
	if !errors.Is(d.CheckLease(), ErrLeaseExpired) {
		t.Fatal("devia estar vencido")
	}
	if err := d.Renew(context.Background(), leaseD); err != nil || d.CheckLease() != nil {
		t.Fatalf("RENEW humano reabre: %v", err)
	}
	if err := d.Restore(context.Background()); err != nil || d.Active() || d.CheckLease() != nil {
		t.Fatalf("RESTORE: %v", err)
	}
	kinds := []domain.EntryKind{}
	for _, e := range cons.entries() {
		kinds = append(kinds, e.Kind)
	}
	want := []domain.EntryKind{domain.KindDegrade, domain.KindRenew, domain.KindRestore}
	if len(kinds) != 3 || kinds[0] != want[0] || kinds[1] != want[1] || kinds[2] != want[2] {
		t.Fatalf("entradas %v", kinds)
	}
	// Nova entrada depois do RESTORE usa uma época nova.
	if err := d.Enter(context.Background()); err != nil || d.Lease.Epoch() != 2 {
		t.Fatalf("época nova esperada: %v %d", err, d.Lease.Epoch())
	}
}

// Enter, Restore, Renew e CheckLease concorrentes: sem corrida e com a invariante
// "gate degradado => lease ativo" valendo quando o Enter retorna.
func TestDegradeConcurrentUse(t *testing.T) {
	d, _, g, _ := newDeg(domain.PolicyAccept)
	g.SetRaftCommit(1)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = d.Enter(context.Background())
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = d.Restore(context.Background())
				_ = d.Renew(context.Background(), leaseD)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = d.CheckLease()
				_ = d.Active()
			}
		}()
	}
	wg.Wait()
}

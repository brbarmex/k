package usecase

import (
	"context"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
)

type pumpRig struct {
	p       *CommitPump
	cons    *fakeConsensus
	g       *domain.Gate
	toApply chan []domain.Entry
	done    chan error
	cancel  context.CancelFunc
}

func startPump(t *testing.T, applyBuf int) *pumpRig {
	t.Helper()
	cons := newFakeConsensus()
	g := &domain.Gate{}
	toApply := make(chan []domain.Entry, applyBuf)
	p := &CommitPump{Consensus: cons, Gate: g, ToApplier: toApply}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
	return &pumpRig{p: p, cons: cons, g: g, toApply: toApply, done: done, cancel: cancel}
}

// commit publica o commit até cada índice (o fake preenche os anteriores com Noop). Não
// bloqueia: o commit é um sinal, não uma fila (ADR-060).
func (r *pumpRig) commit(t *testing.T, idxs ...domain.Index) {
	t.Helper()
	var es []domain.Entry
	for _, i := range idxs {
		es = append(es, domain.Entry{Index: i, Term: 1})
	}
	r.cons.commitEntries(es...)
}

// nextApplied devolve o próximo lote entregue ao Applier.
func (r *pumpRig) nextApplied(t *testing.T) []domain.Entry {
	t.Helper()
	select {
	case b := <-r.toApply:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("nenhum lote chegou ao Applier")
		return nil
	}
}

func waitGate(t *testing.T, g *domain.Gate, idx domain.Index) {
	t.Helper()
	ctx, cancel := timeout(2 * time.Second)
	defer cancel()
	if err := g.WaitFor(ctx, idx); err != nil {
		t.Fatalf("gate não chegou a %d (está em %d)", idx, g.Index())
	}
}

// ADR-060: o gate (gate = commit) acompanha o commit publicado mesmo com o
// Applier parado. Antes, o gate só andava quando o lote entrava na fila do Applier, e o
// teste de carga de 14/10 mostrou 73 mil entradas de "commit sem gate".
func TestPOCGateFollowsCommitWhileApplierIsStuck(t *testing.T) {
	r := startPump(t, 0) // ninguém lê o Applier
	for i := domain.Index(1); i <= 50; i++ {
		r.commit(t, i)
	}
	waitGate(t, r.g, 50)
}

// ADR-060, item 5: a bomba começa em From e entrega em ordem, sem buraco.
func TestPumpStartsAtFrom(t *testing.T) {
	cons := newFakeConsensus()
	cons.commitEntries(domain.Entry{Index: 10, Term: 1})
	toApply := make(chan []domain.Entry, 10)
	p := &CommitPump{Consensus: cons, Gate: &domain.Gate{}, ToApplier: toApply, From: 7}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Run(ctx) }()
	select {
	case b := <-toApply:
		if b[0].Index != 7 || b[len(b)-1].Index != 10 {
			t.Fatalf("lote = %d..%d, quer 7..10", b[0].Index, b[len(b)-1].Index)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nada entregue")
	}
	waitUntil(t, func() bool { return p.Delivered() == 10 }, "Delivered devia chegar a 10")
}

func TestResumeFrom(t *testing.T) {
	for _, tc := range []struct{ progress, want domain.Index }{
		{0, 1},     // partida do zero ou sem progresso salvo
		{40, 40},   // reentrega a entrada do progresso
		{100, 100}, // itens já gravados da entrada do progresso são pulados pelo applier
	} {
		if got := ResumeFrom(tc.progress); got != tc.want {
			t.Fatalf("ResumeFrom(%d) = %d, quer %d", tc.progress, got, tc.want)
		}
	}
}

// Sinal sem commit novo não derruba nem faz o pump entregar lote vazio.
func TestSignalWithoutNewCommitIsHarmless(t *testing.T) {
	r := startPump(t, 10)
	r.commit(t) // nada novo
	r.commit(t, 3)
	if b := r.nextApplied(t); len(b) == 0 {
		t.Fatal("lote vazio entregue ao Applier")
	}
	select {
	case err := <-r.done:
		t.Fatalf("o pump parou: %v", err)
	default:
	}
}

// O-42, item 1 (DC-04, FL-07): a partida começa em reconstruindo com o progresso atrás do
// disco retido; a partida do zero e o progresso na cauda começam em normal.
func TestStartsRebuilding(t *testing.T) {
	for _, tc := range []struct {
		progress, first domain.Index
		rebuild         bool
	}{
		{0, 2, false},       // partida do zero
		{0, 1, false},       // só o snapshot sintético
		{9000, 5000, false}, // na cauda
		{4999, 5000, false}, // a próxima entrada é a primeira retida
		{4998, 5000, true},  // progresso falso atrás do disco
		{10, 5000, true},
		{0, 5000, true}, // progresso perdido com o disco já liberado
	} {
		if got := StartsRebuilding(tc.progress, tc.first); got != tc.rebuild {
			t.Errorf("StartsRebuilding(%d, %d) = %v, quer %v", tc.progress, tc.first, got, tc.rebuild)
		}
	}
}

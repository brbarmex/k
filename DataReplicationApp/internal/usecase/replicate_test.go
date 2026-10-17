package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

type pumpRig struct {
	p       *CommitPump
	cons    *fakeConsensus
	g       *domain.Gate
	lease   *domain.Lease
	toApply chan []domain.Entry
	done    chan error
	cancel  context.CancelFunc
}

func startPump(t *testing.T, applyBuf int) *pumpRig {
	t.Helper()
	cons := newFakeConsensus()
	g := &domain.Gate{}
	toApply := make(chan []domain.Entry, applyBuf)
	lease := &domain.Lease{}
	p := &CommitPump{Consensus: cons, Gate: g, Lease: lease, ToApplier: toApply}
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
	return &pumpRig{p: p, cons: cons, g: g, lease: lease, toApply: toApply, done: done, cancel: cancel}
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

func (r *pumpRig) learner(t *testing.T, id string, term domain.Term, idx domain.Index) {
	t.Helper()
	select {
	case r.cons.events <- port.LearnerProgress{Term: term, Index: idx, Learner: id}:
	case <-time.After(2 * time.Second):
		t.Fatal("o pump não aceitou o evento do learner")
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

// O defeito principal: o commit chega ANTES da confirmação da Azure e nenhum commit novo
// vem depois. Com o gate alimentado só por lote, a escrita ficava esperando até o
// timeout. Com evento, cada resposta de learner libera o gate na hora.
func TestGateIsFedByLearnerEventsWithoutNewCommit(t *testing.T) {
	r := startPump(t, 10)
	r.commit(t, 5)
	r.nextApplied(t)
	if r.g.Index() != 0 {
		t.Fatalf("sem Azure o gate não passa de 0, veio %d", r.g.Index())
	}
	r.learner(t, "az1", 1, 5)
	r.learner(t, "az2", 1, 5)
	waitGate(t, r.g, 5) // nenhum commit novo foi enviado
}

func TestGateAdvancesWhileApplierIsStuck(t *testing.T) {
	r := startPump(t, 0) // canal sem buffer, ninguém lê: applier parado
	r.commit(t, 9)
	time.Sleep(20 * time.Millisecond)
	r.learner(t, "az1", 1, 9)
	r.learner(t, "az2", 1, 9)
	waitGate(t, r.g, 9)
}

// ADR-060: o gate da POC (gate = commit) acompanha o commit publicado mesmo com o
// Applier parado. Antes, o gate só andava quando o lote entrava na fila do Applier, e o
// teste de carga de 14/10 mostrou 73 mil entradas de "commit sem gate".
func TestPOCGateFollowsCommitWhileApplierIsStuck(t *testing.T) {
	r := startPump(t, 0) // ninguém lê o Applier
	r.g.SetAzureOff(true)
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

// Só conta o termo atual: um evento de termo antigo (sufixo de líder antiga) é ignorado,
// e líder nova só adota o confirmado no termo novo.
func TestPumpIgnoresStaleTermAndRebuildsOnNewTerm(t *testing.T) {
	r := startPump(t, 10)
	r.commit(t, 10)
	r.learner(t, "az1", 1, 10)
	r.learner(t, "az2", 1, 10)
	waitGate(t, r.g, 10)

	r.cons.setTerm(2)
	r.commit(t, 20)
	r.learner(t, "az1", 1, 20) // atrasado, do termo antigo
	r.learner(t, "az2", 1, 20)
	time.Sleep(30 * time.Millisecond)
	if r.g.Index() != 10 {
		t.Fatalf("progresso do termo antigo foi adotado: %d", r.g.Index())
	}
	r.learner(t, "az1", 2, 20)
	r.learner(t, "az2", 2, 18)
	waitGate(t, r.g, 18)
	if r.g.Index() != 18 {
		t.Fatalf("esperado 18, veio %d", r.g.Index())
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

// Canal de eventos fechado devolve erro (o motor parou) em vez de girar em falso.
func TestClosedEventsChannelReturnsError(t *testing.T) {
	cons := newFakeConsensus()
	p := &CommitPump{Consensus: cons, Gate: &domain.Gate{}, ToApplier: make(chan []domain.Entry, 1)}
	done := make(chan error, 1)
	go func() { done <- p.Run(context.Background()) }()
	close(cons.events)
	select {
	case err := <-done:
		if !errors.Is(err, ErrStreamClosed) {
			t.Fatalf("esperado ErrStreamClosed, veio %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o pump não terminou com o canal fechado")
	}
}

func TestPumpStopsOnContext(t *testing.T) {
	r := startPump(t, 1)
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("não parou com o contexto")
	}
}

// O pump alimenta t_c: duas confirmações da Azure (heartbeat incluído) formam a maioria, e
// o termo novo descarta os contatos do antigo.
func TestPumpFeedsLastContactToLease(t *testing.T) {
	r := startPump(t, 10)
	t0 := time.Unix(5_000_000, 0)
	r.lease.Mark(1, time.Minute)
	send := func(id string, term domain.Term, at time.Time) {
		select {
		case r.cons.events <- port.LearnerProgress{Term: term, Learner: id, SentAt: at}: // heartbeat: sem índice novo
		case <-time.After(2 * time.Second):
			t.Fatal("o pump não aceitou o evento")
		}
	}
	send("az1", 1, t0)
	send("az2", 1, t0)
	deadline := time.Now().Add(2 * time.Second)
	for r.lease.Check(t0.Add(time.Second)) != nil {
		if time.Now().After(deadline) {
			t.Fatal("o contato com a maioria Azure não chegou ao lease")
		}
		time.Sleep(time.Millisecond)
	}
	r.cons.setTerm(2)
	send("az1", 1, t0.Add(time.Second)) // atrasado, do termo antigo (e o pump já viu o termo 2)
	time.Sleep(30 * time.Millisecond)
	if r.lease.Check(t0.Add(2*time.Second)) == nil {
		t.Fatal("líder nova não herda t_c do termo antigo")
	}
}

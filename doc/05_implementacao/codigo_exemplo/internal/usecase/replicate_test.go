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

func (r *pumpRig) commit(t *testing.T, idxs ...domain.Index) {
	t.Helper()
	var es []domain.Entry
	for _, i := range idxs {
		es = append(es, domain.Entry{Index: i, Term: 1})
	}
	select {
	case r.cons.committed <- port.CommitBatch{Entries: es}:
	case <-time.After(2 * time.Second):
		t.Fatal("o pump não aceitou o lote")
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
	<-r.toApply
	if r.g.Index() != 0 {
		t.Fatalf("sem Azure o gate não passa de 0, veio %d", r.g.Index())
	}
	r.learner(t, "az1", 1, 5)
	r.learner(t, "az2", 1, 5)
	waitGate(t, r.g, 5) // nenhum commit novo foi enviado
}

func TestGateAdvancesWhileApplierIsStuck(t *testing.T) {
	r := startPump(t, 0) // canal sem buffer, ninguém lê: applier parado
	go func() { r.cons.committed <- port.CommitBatch{Entries: []domain.Entry{{Index: 9, Term: 1}}} }()
	time.Sleep(20 * time.Millisecond)
	r.learner(t, "az1", 1, 9)
	r.learner(t, "az2", 1, 9)
	waitGate(t, r.g, 9)
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

// Lote vazio não derruba o pump (antes: b.Entries[len(b.Entries)-1]).
func TestEmptyBatchDoesNotPanic(t *testing.T) {
	r := startPump(t, 10)
	r.commit(t) // lote vazio
	r.commit(t, 3)
	<-r.toApply
	select {
	case err := <-r.done:
		t.Fatalf("o pump parou: %v", err)
	default:
	}
}

// Canal fechado devolve erro (o motor parou) em vez de girar em falso ou entrar em pânico.
func TestClosedChannelsReturnError(t *testing.T) {
	for _, which := range []string{"committed", "events"} {
		cons := newFakeConsensus()
		p := &CommitPump{Consensus: cons, Gate: &domain.Gate{}, ToApplier: make(chan []domain.Entry, 1)}
		done := make(chan error, 1)
		go func() { done <- p.Run(context.Background()) }()
		if which == "committed" {
			close(cons.committed)
		} else {
			close(cons.events)
		}
		select {
		case err := <-done:
			if !errors.Is(err, ErrStreamClosed) {
				t.Fatalf("%s: esperado ErrStreamClosed, veio %v", which, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: o pump não terminou com o canal fechado", which)
		}
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

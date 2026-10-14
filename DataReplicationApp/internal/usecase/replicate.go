package usecase

import (
	"context"
	"errors"
	"sync"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// ErrStreamClosed: um canal do motor de consenso fechou (o motor parou). É fatal para o
// processo: sem eventos o gate nunca mais avança.
var ErrStreamClosed = errors.New("canal do consenso fechado")

// CommitPump liga o Raft ao resto, em DUAS goroutines independentes:
//
//  1. progresso: cada resposta de learner da Azure (evento) alimenta o gate no momento
//     em que chega, com termo e identidade (ADR-046), e registra o contato (t_c) que sustenta
//     o lease do modo degradado (ADR-044). Não depende de haver commit novo,
//     nem de o disco do log de replicação ou o applier estarem lentos;
//  2. commits: cada lote commitado vai (a) para o gate (commit Raft), (b) para o Applier,
//     da memória, e (c) para o log de replicação.
//
// Ponto de atenção: Log.Append continua SÍNCRONO dentro da goroutine de commits (gravação
// sem fsync no log de replicação, ADR-055). Ele atrasa a entrega do próximo lote ao Applier, mas não atrasa o
// gate, que tem goroutine própria. Na versão final o Append roda numa goroutine com
// fila limitada (backpressure), e o Applier espera o gate, não o Append.
type CommitPump struct {
	Consensus port.ConsensusEngine
	Log       port.ReplicationLog
	Gate      *domain.Gate
	Lease     *domain.Lease // opcional: recebe t_c (último contato com a maioria Azure)
	ToApplier chan<- []domain.Entry
}

func (p *CommitPump) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := p.runProgress(ctx); err != nil {
			cancel(err)
		}
	}()
	err := p.runCommits(ctx)
	cancel(err)
	wg.Wait()
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

func (p *CommitPump) runProgress(ctx context.Context) error {
	events := p.Consensus.LearnerEvents()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return ErrStreamClosed
			}
			// O termo vem do motor antes do evento: líder nova descarta o progresso
			// herdado e só adota o confirmado no termo novo (ADR-046, item 3).
			term := p.Consensus.Term()
			p.Gate.SetTerm(term)
			p.Gate.SetAzureDurable(ev.Term, ev.Index, ev.Learner)
			if p.Lease != nil { // heartbeat também conta: é contato, mesmo sem índice novo
				p.Lease.SetTerm(term)
				p.Lease.ObserveContact(ev.Term, ev.Learner, ev.SentAt)
			}
		}
	}
}

func (p *CommitPump) runCommits(ctx context.Context) error {
	committed := p.Consensus.Committed()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b, ok := <-committed:
			if !ok {
				return ErrStreamClosed
			}
			if len(b.Entries) == 0 {
				continue // lote vazio: nada a fazer (e nada de b.Entries[len-1])
			}
			p.Gate.SetTerm(p.Consensus.Term())
			p.Gate.SetRaftCommit(b.Entries[len(b.Entries)-1].Index)
			select {
			case p.ToApplier <- b.Entries: // memória: não espera o disco
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := p.Log.Append(ctx, b.Entries); err != nil {
				return err // erro de escrita no log de replicação é fatal (ADR-055)
			}
		}
	}
}

package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// ErrStreamClosed: o canal de entradas do applier fechou. É fatal para o processo.
var ErrStreamClosed = errors.New("canal do consenso fechado")

// CommitPump liga o Raft ao resto, em DUAS goroutines independentes:
//
//  1. gate: cada avanço do commit publicado pelo Raft vai direto ao gate (ADR-060). O gate
//     não depende da fila do applier;
//  2. commits: lê as entradas commitadas POR FAIXA da memória do Raft, a partir de From,
//     e as entrega ao Applier.
//
// O commit é um sinal, não uma fila (ADR-060): o laço do Raft nunca espera esta bomba.
// Se o applier não acompanha, quem espera é a goroutine de commits, e o atraso fica nas
// entradas que o Raft já guarda; a admissão da escrita (APPLY_LAG_MAX) segura a entrada
// de trabalho novo.
type CommitPump struct {
	Consensus port.ConsensusEngine
	Gate      *domain.Gate
	ToApplier chan<- []domain.Entry
	// From é o primeiro índice a entregar (ADR-060, item 5): ResumeFrom na partida. Abaixo
	// do primeiro índice retido em disco, começa nele: tudo o que está antes já foi
	// aplicado com o progresso durável, porque o disco nunca é liberado além dele (G1).
	From domain.Index

	delivered atomic.Uint64 // último índice entregue ao Applier
}

// readBytes é o teto de cada leitura por faixa do log (RAM ou disco).
const readBytes = 1 << 20

// ResumeFrom é o primeiro índice que a bomba entrega na partida (ADR-060, item 5): o
// índice do progresso do applier, ou 1 sem progresso salvo. A entrada do progresso é
// reentregue inteira: o applier pula os itens já gravados pela ordem salva, e o replay,
// lido do WAL do Raft, é idempotente pela versão.
func ResumeFrom(progressIdx domain.Index) domain.Index {
	return max(progressIdx, 1)
}

// Delivered é o último índice entregue ao Applier (métrica dr_commit_undelivered).
func (p *CommitPump) Delivered() domain.Index { return domain.Index(p.delivered.Load()) }

func (p *CommitPump) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.runGate(ctx)
	}()
	err := p.runCommits(ctx)
	cancel()
	wg.Wait()
	return err
}

// runGate leva cada avanço do commit ao gate, sem passar pela fila do applier (ADR-060).
func (p *CommitPump) runGate(ctx context.Context) {
	for {
		changed := p.Consensus.CommitChanged()
		p.Gate.SetRaftCommit(p.Consensus.CommitIndex())
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

func (p *CommitPump) runCommits(ctx context.Context) error {
	next := max(p.From, p.Consensus.FirstIndex(), 1)
	p.delivered.Store(uint64(next - 1))
	started := false // já entregou algo nesta execução
	for {
		changed := p.Consensus.CommitChanged() // antes de ler o índice: não perde avanço
		commit := p.Consensus.CommitIndex()
		if next > commit {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				continue
			}
		}
		ents, err := p.Consensus.CommittedEntries(next, commit, readBytes)
		if errors.Is(err, port.ErrNotRetained) && !started {
			// Na partida, sem a entrada next na RAM nem no disco, a célula está atrás da
			// cauda e vai receber o marcador (O-34, RT-18): espera o commit avançar.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				continue
			}
		}
		if err != nil {
			return err // o log perdeu entradas não entregues: bug, fatal (G1)
		}
		if len(ents) == 0 {
			continue
		}
		if ents[0].Index != next {
			return fmt.Errorf("commitpump: esperava o índice %d, veio %d", next, ents[0].Index)
		}
		select {
		case p.ToApplier <- ents: // pode esperar o applier: o laço do Raft não espera
		case <-ctx.Done():
			return ctx.Err()
		}
		next = ents[len(ents)-1].Index + 1
		p.delivered.Store(uint64(next - 1))
		started = true
	}
}

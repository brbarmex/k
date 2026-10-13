package usecase

import (
	"context"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// CommitPump liga o Raft ao resto: cada lote commitado vai (1) para o gate,
// (2) para o Applier, da memória, e (3) para o log de replicação, em lote.
type CommitPump struct {
	Consensus port.ConsensusEngine
	Log       port.ReplicationLog
	Gate      *domain.Gate
	ToApplier chan<- []domain.Entry
}

func (p *CommitPump) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b := <-p.Consensus.Committed():
			last := b.Entries[len(b.Entries)-1].Index
			p.Gate.SetRaftCommit(last)
			p.Gate.SetAzureDurable(p.Consensus.LearnerDurable())
			p.ToApplier <- b.Entries // memória: não espera o disco
			// Esboço: na versão final o Append roda numa goroutine própria, com fila
			// limitada, para não atrasar a entrega ao Applier.
			if err := p.Log.Append(ctx, b.Entries); err != nil {
				return err // falha de disco do log de replicação é fatal
			}
		}
	}
}

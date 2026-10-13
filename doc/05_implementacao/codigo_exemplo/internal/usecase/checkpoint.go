package usecase

import (
	"context"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// Checkpointer liga os dois logs (ADR-055), fora do caminho do ACK. A cada Interval:
//
//  1. Sync do log de replicação: um fsync que publica o DurableIndex;
//  2. compacta o log do Raft até domain.RaftCompactLimit (nunca além do durável);
//  3. se Retention estiver ligado, trunca o log de replicação até
//     domain.ReplogTruncateLimit (applied, maioria Azure, F do rebuild).
//
// Erro de Sync é fatal para o processo, como o fsync do Raft (ADR-034).
type Checkpointer struct {
	Log       port.ReplicationLog
	Raft      port.RaftCompactor
	RetainMax uint64 // folga do Raft para pares pouco atrasados, em entradas
	Interval  time.Duration
	// Retention devolve as entradas da regra de truncamento do log de replicação.
	Retention func() (applied, azureDurable domain.Index, azureInWindow bool, rebuildFrom domain.Index)
}

func (c *Checkpointer) Run(ctx context.Context) error {
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := c.Tick(ctx); err != nil {
				return err
			}
		}
	}
}

// Tick faz um ciclo; exposto para os testes.
func (c *Checkpointer) Tick(ctx context.Context) error {
	durable, err := c.Log.Sync(ctx)
	if err != nil {
		return err
	}
	if err := c.Raft.Compact(domain.RaftCompactLimit(durable, c.Raft.MinPeerMatch(), c.RetainMax)); err != nil {
		return err
	}
	if c.Retention == nil {
		return nil
	}
	return c.Log.CompactPrefix(ctx, domain.ReplogTruncateLimit(c.Retention()))
}

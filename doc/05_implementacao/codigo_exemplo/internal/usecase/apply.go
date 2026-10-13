package usecase

import (
	"context"
	"fmt"
	"sync"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// Applier materializa o log no banco da célula (fluxo do apply, D04).
//
//   - Recebe as entradas commitadas PELA MEMÓRIA, do CommitPump. No fluxo normal não lê
//     o log de replicação; só o replay da partida e o catch-up leem do disco.
//   - Aplica em série e nunca além do gate (ADR-057): applied <= gate sempre.
//   - Cada item é uma escrita condicional pela versão (= índice), então o replay é
//     idempotente (ADR-015, ADR-035).
//   - Depois de cada item grava o progresso (índice, ordem) sem fsync (ADR-048, ADR-055).
//     Perder o progresso numa queda de host só alonga o replay.
//   - Erro do banco num item para o shard: o SKIP é sempre humano (ADR-038).
//
// Na célula follower o gate é o índice que a líder propaga; o esboço usa o mesmo Gate.
type Applier struct {
	Gate     *domain.Gate
	Store    port.StateStore
	Progress port.ApplyProgress
	In       <-chan []domain.Entry

	mu      sync.Mutex
	applied domain.Index
	changed chan struct{} // fechado e trocado a cada avanço de applied
}

var _ port.AppliedWaiter = (*Applier)(nil)

// Run consome as entradas até o contexto acabar ou um item falhar.
func (a *Applier) Run(ctx context.Context) error {
	pIdx, pOrd, err := a.Progress.LoadProgress()
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case batch, ok := <-a.In:
			if !ok {
				return ErrStreamClosed
			}
			for _, e := range batch {
				if e.Index <= a.Applied() {
					continue // replay: entrada já aplicada nesta execução
				}
				if err := a.Gate.WaitFor(ctx, e.Index); err != nil {
					return err
				}
				if e.Kind == domain.KindWrite {
					for i := range e.Ops {
						ord := uint32(i + 1)
						if e.Index < pIdx || (e.Index == pIdx && ord <= pOrd) {
							continue // aplicado antes da queda; reaplicar também seria inofensivo
						}
						op := []port.VersionedOp{{Op: e.Ops[i], Version: e.Index}}
						if err := a.Store.ApplyBatch(ctx, op); err != nil {
							return fmt.Errorf("apply parado em (%d, %d): %w", e.Index, ord, err)
						}
						if err := a.Progress.SaveProgress(e.Index, ord); err != nil {
							return err
						}
					}
				}
				a.advance(e.Index)
			}
		}
	}
}

// Applied é o maior índice já aplicado no banco da célula.
func (a *Applier) Applied() domain.Index {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applied
}

func (a *Applier) advance(idx domain.Index) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applied = idx
	if a.changed != nil {
		close(a.changed)
		a.changed = nil
	}
}

// WaitApplied implementa port.AppliedWaiter: o ACK da líder espera o apply local (6.9).
func (a *Applier) WaitApplied(ctx context.Context, idx domain.Index) error {
	for {
		a.mu.Lock()
		if a.applied >= idx {
			a.mu.Unlock()
			return nil
		}
		if a.changed == nil {
			a.changed = make(chan struct{})
		}
		ch := a.changed
		a.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

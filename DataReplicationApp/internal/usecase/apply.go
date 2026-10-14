package usecase

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog"

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
//   - Erro transitório do banco é repetido com backoff, sem limite (AP-07).
//   - Erro permanente num item ESTACIONA o applier do shard nesta célula: não pula, não
//     derruba o processo, e o estado fica visível em Health (/readyz e /status). O SKIP
//     é sempre humano (ADR-038).
//
// Na célula follower o gate é o índice que a líder propaga (G-06).
type Applier struct {
	Gate     *domain.Gate
	Store    port.StateStore
	Progress port.ApplyProgress
	In       <-chan []domain.Entry
	Backoff  Backoff // padrão 100 ms a 5 s
	Log      zerolog.Logger

	mu      sync.Mutex
	applied domain.Index
	changed chan struct{} // fechado e trocado a cada avanço de applied
	health  ApplyHealth
}

// Backoff é a espera exponencial entre repetições de erro transitório.
type Backoff struct {
	Min, Max time.Duration
}

// ApplyHealth diz se o applier está estacionado num item venenoso e onde.
type ApplyHealth struct {
	Stopped bool
	Index   domain.Index
	Ord     uint32
	Err     string
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
						if err := a.applyItem(ctx, op, e.Index, ord); err != nil {
							if ctx.Err() != nil {
								return ctx.Err()
							}
							return a.park(ctx, e.Index, ord, err)
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

// applyItem aplica um item, repetindo sem limite o erro transitório (AP-07). Devolve o
// erro permanente, ou o erro do contexto.
func (a *Applier) applyItem(ctx context.Context, op []port.VersionedOp, idx domain.Index, ord uint32) error {
	lo, hi := a.Backoff.Min, a.Backoff.Max
	if lo <= 0 {
		lo = 100 * time.Millisecond
	}
	if hi < lo {
		hi = 5 * time.Second
	}
	wait := lo
	for {
		err := a.Store.ApplyBatch(ctx, op)
		if err == nil || !errors.Is(err, port.ErrStoreTransient) {
			return err
		}
		a.Log.Warn().Err(err).Uint64("indice", uint64(idx)).Uint32("ordem", ord).Dur("espera", wait).
			Msg("erro transitório no apply, repetindo")
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		wait = min(wait*2, hi)
	}
}

// park estaciona o applier no item venenoso até o contexto acabar (ADR-038).
func (a *Applier) park(ctx context.Context, idx domain.Index, ord uint32, err error) error {
	a.mu.Lock()
	a.health = ApplyHealth{Stopped: true, Index: idx, Ord: ord, Err: err.Error()}
	a.mu.Unlock()
	a.Log.Error().Err(err).Uint64("indice", uint64(idx)).Uint32("ordem", ord).
		Msg("applier parado: saída humana (ADR-038)")
	<-ctx.Done()
	return ctx.Err()
}

// Health devolve o estado do applier (estacionado ou não).
func (a *Applier) Health() ApplyHealth {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.health
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

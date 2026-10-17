package usecase

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// workerQueue é a fila de itens de cada worker. Cheia, o despacho espera: é o limite de
// itens a caminho do banco (Workers × workerQueue).
const workerQueue = 256

// Applier materializa o log no banco da célula (fluxo do apply, D04).
//
//   - Recebe as entradas commitadas PELA MEMÓRIA, do CommitPump. Só o replay da partida
//     relê o WAL do Raft.
//   - Nunca aplica além do gate (ADR-057): o despacho de uma entrada espera
//     gate >= índice, então applied <= gate sempre.
//   - Grava em PARALELO POR CHAVE (ADR-058): Workers goroutines, e cada item vai para o
//     worker escolhido pelo hash de (tabela, chave). A mesma chave cai sempre no mesmo
//     worker, na ordem do log; chaves diferentes não esperam umas pelas outras.
//   - applied é a MARCA D'ÁGUA: o maior índice tal que ele e todos os anteriores estão no
//     banco. Itens de índices maiores podem já estar gravados; o ACK da líder e a leitura
//     forte só olham a marca d'água.
//   - Cada item é uma escrita condicional pela versão (= índice), então o replay é
//     idempotente (ADR-015, ADR-035).
//   - Cada avanço da marca d'água grava o progresso (índice, ordem) sem fsync (ADR-048).
//     Perder o progresso numa queda de host só alonga o replay.
//   - Erro transitório do banco é repetido com backoff, sem limite (AP-07).
//   - Erro permanente num item ESTACIONA o applier do shard nesta célula: o despacho para,
//     itens anteriores ao venenoso terminam, posteriores não são gravados (salvo os que já
//     estavam a caminho do banco), e a marca d'água para antes dele. Não pula, não derruba
//     o processo, e o estado fica visível em Health (/readyz e /status). O SKIP é sempre
//     humano (ADR-038).
//
// Na célula follower o gate é o índice que a líder propaga (G-06).
type Applier struct {
	Gate     *domain.Gate
	Store    port.StateStore
	Progress port.ApplyProgress
	In       <-chan []domain.Entry
	Workers  int     // APPLY_WORKERS; 0 ou 1 = um item por vez
	Backoff  Backoff // padrão 100 ms a 5 s
	Log      zerolog.Logger
	Metrics  port.Metrics // opcional

	mu      sync.Mutex
	applied domain.Index  // marca d'água
	pending []*inflight   // entradas despachadas e não concluídas, em ordem de índice
	changed chan struct{} // fechado e trocado a cada avanço de applied
	health  ApplyHealth
	parked  chan struct{} // fechado quando o applier estaciona
	queued  atomic.Int64  // itens despachados e ainda não concluídos
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

// inflight é uma entrada despachada: a marca d'água só passa dela quando left chega a 0.
type inflight struct {
	idx  domain.Index
	ops  uint32 // número de itens da entrada (ordem do último)
	left int    // itens ainda não concluídos
}

// task é um item a gravar.
type task struct {
	ent *inflight
	ord uint32
	op  port.VersionedOp
}

var _ port.AppliedWaiter = (*Applier)(nil)

// Run consome as entradas até o contexto acabar, o canal fechar ou o progresso falhar.
// Estacionado, fica parado até o contexto acabar.
func (a *Applier) Run(ctx context.Context) error {
	pIdx, pOrd, err := a.Progress.LoadProgress()
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.parked == nil {
		a.parked = make(chan struct{})
	}
	a.mu.Unlock()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	queues := make([]chan task, a.WorkerCount())
	var wg sync.WaitGroup
	for i := range queues {
		queues[i] = make(chan task, workerQueue)
		wg.Add(1)
		go func(q <-chan task) {
			defer wg.Done()
			a.work(ctx, q, cancel)
		}(queues[i])
	}
	err = a.dispatch(ctx, queues, pIdx, pOrd)
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		err = cause // falha de um worker (progresso), não o cancelamento normal
	}
	cancel(err)
	wg.Wait()
	return err
}

// dispatch lê as entradas em ordem, espera o gate de cada uma e entrega os itens aos
// workers. Itens até o progresso salvo (pIdx, pOrd) já estavam no banco antes da queda.
func (a *Applier) dispatch(ctx context.Context, queues []chan task, pIdx domain.Index, pOrd uint32) error {
	var last domain.Index // maior índice já despachado nesta execução
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-a.parked:
			<-ctx.Done() // estacionado: não despacha mais nada (ADR-038)
			return context.Cause(ctx)
		case batch, ok := <-a.In:
			if !ok {
				return ErrStreamClosed
			}
			for _, e := range batch {
				if e.Index <= last {
					continue // replay: entrada já despachada nesta execução
				}
				if err := a.Gate.WaitFor(ctx, e.Index); err != nil {
					return err
				}
				ent := &inflight{idx: e.Index}
				var tasks []task
				if e.Kind == domain.KindWrite {
					ent.ops = uint32(len(e.Ops))
					for i := range e.Ops {
						ord := uint32(i + 1)
						if e.Index < pIdx || (e.Index == pIdx && ord <= pOrd) {
							continue // aplicado antes da queda; reaplicar também seria inofensivo
						}
						tasks = append(tasks, task{ent: ent, ord: ord, op: port.VersionedOp{Op: e.Ops[i], Version: e.Index}})
					}
				}
				ent.left = len(tasks)
				if err := a.track(ent); err != nil {
					return err
				}
				for _, t := range tasks {
					a.queued.Add(1)
					select {
					case queues[keySlot(t.op.Op, len(queues))] <- t:
					case <-ctx.Done():
						return context.Cause(ctx)
					case <-a.parked:
						<-ctx.Done()
						return context.Cause(ctx)
					}
				}
				last = e.Index
			}
		}
	}
}

// work grava os itens de uma fila, um por vez e na ordem em que chegaram.
func (a *Applier) work(ctx context.Context, q <-chan task, fail context.CancelCauseFunc) {
	m := port.MetricsOrNop(a.Metrics)
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-q:
			if a.beyondPark(t) {
				a.queued.Add(-1) // depois do item venenoso: não grava (ADR-038)
				continue
			}
			start := time.Now()
			err := a.applyItem(ctx, []port.VersionedOp{t.op}, t.ent.idx, t.ord)
			if err != nil {
				a.queued.Add(-1)
				if ctx.Err() != nil {
					return
				}
				a.park(t.ent.idx, t.ord, err)
				continue // os itens seguintes desta fila vêm depois dele e serão descartados
			}
			m.ApplyItem(time.Since(start))
			a.queued.Add(-1)
			if err := a.done(t.ent); err != nil {
				fail(err)
				return
			}
		}
	}
}

// keySlot escolhe o worker pela tabela e pela chave canônica (FNV-1a): a mesma chave
// sempre no mesmo worker, para manter a ordem por chave que o DELETE exige (X06).
func keySlot(op domain.Op, n int) int {
	if n <= 1 {
		return 0
	}
	h := uint64(14695981039346656037)
	mix := func(b byte) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	for i := 0; i < len(op.Table); i++ {
		mix(op.Table[i])
	}
	mix(0) // separador: ("ab", "c") e ("a", "bc") não colidem
	for _, b := range op.Key {
		mix(b)
	}
	return int(h % uint64(n))
}

// track registra a entrada despachada. Sem itens a gravar, ela já está concluída.
func (a *Applier) track(ent *inflight) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = append(a.pending, ent)
	return a.settleLocked()
}

// done marca um item concluído e avança a marca d'água.
func (a *Applier) done(ent *inflight) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	ent.left--
	return a.settleLocked()
}

// settleLocked avança a marca d'água pelas entradas já concluídas, em ordem de índice,
// e grava o progresso uma vez por avanço.
func (a *Applier) settleLocked() error {
	var head *inflight
	for len(a.pending) > 0 && a.pending[0].left == 0 {
		head = a.pending[0]
		a.pending[0] = nil
		a.pending = a.pending[1:]
	}
	if head == nil {
		return nil
	}
	a.applied = head.idx
	if a.changed != nil {
		close(a.changed)
		a.changed = nil
	}
	return a.Progress.SaveProgress(head.idx, head.ops)
}

// beyondPark diz se o item vem depois do item venenoso em que o applier estacionou.
func (a *Applier) beyondPark(t task) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.health
	return h.Stopped && (t.ent.idx > h.Index || (t.ent.idx == h.Index && t.ord > h.Ord))
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

// park estaciona o applier no item venenoso (ADR-038). Com dois itens venenosos em
// workers diferentes, vale o primeiro na ordem do log: é onde a marca d'água para.
func (a *Applier) park(idx domain.Index, ord uint32, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h := a.health; h.Stopped && (h.Index < idx || (h.Index == idx && h.Ord < ord)) {
		return
	}
	first := !a.health.Stopped
	a.health = ApplyHealth{Stopped: true, Index: idx, Ord: ord, Err: err.Error()}
	if first {
		close(a.parked)
	}
	if a.changed != nil { // acorda quem espera: quem está no item venenoso ou depois dele desiste
		close(a.changed)
		a.changed = nil
	}
	a.Log.Error().Err(err).Uint64("indice", uint64(idx)).Uint32("ordem", ord).
		Msg("applier parado: saída humana (ADR-038)")
}

// Health devolve o estado do applier (estacionado ou não).
func (a *Applier) Health() ApplyHealth {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.health
}

// Applied é a marca d'água: o maior índice que, com todos os anteriores, está no banco.
func (a *Applier) Applied() domain.Index {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applied
}

// WorkerCount é o número de workers efetivo (pelo menos 1).
func (a *Applier) WorkerCount() int { return max(a.Workers, 1) }

// InFlight é quantos itens foram despachados e ainda não terminaram.
func (a *Applier) InFlight() int { return int(a.queued.Load()) }

// WaitApplied implementa port.AppliedWaiter: o ACK da líder espera o apply local (6.9).
// Com o applier estacionado, um índice no item venenoso ou depois dele nunca será
// alcançado: devolve ErrApplierStopped na hora, em vez de esperar o prazo (ADR-049, item 4).
// Índices antes do item venenoso continuam sendo esperados, porque a marca d'água ainda
// chega até logo antes dele.
func (a *Applier) WaitApplied(ctx context.Context, idx domain.Index) error {
	for {
		a.mu.Lock()
		if a.applied >= idx {
			a.mu.Unlock()
			return nil
		}
		if h := a.health; h.Stopped && idx >= h.Index {
			a.mu.Unlock()
			return ErrApplierStopped
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

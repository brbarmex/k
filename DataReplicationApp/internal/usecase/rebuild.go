package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// ErrLeaderRebuild: a reconstrução manual foi pedida na líder (409 no /admin/reconstruir).
var ErrLeaderRebuild = errors.New("a líder não se reconstrói")

// Rebuild é a reconstrução de uma célula fora da janela (AP-18, DC-04, DC-11): copia as
// tabelas de uma célula em normal com a versão de cada item e grava **na tabela que já
// existe** pelo mesmo PutItem condicional do apply (AP-04). Nada é apagado nem recriado
// (DC-11, no lugar da tabela sombra da D8 e da recriação do AP-18): um item só anda para
// frente, e uma tentativa abortada deixa a tabela pelo menos como estava. Um item apagado
// na fonte continuaria vivo aqui; hoje não há DeleteItem (AP-19).
//
// No fim, grava o progresso (F, OrdComplete) com fsync e devolve ErrRestartAfterRebuild: o
// processo reinicia e a partida segue pela recuperação a partir de F + 1 (DC-04).
type Rebuild struct {
	Self    uint64
	Peers   []uint64
	Client  port.RecoveryClient
	Catalog port.Catalog
	// Identity é o cluster_id e o epoch do marcador local (DC-11).
	Identity func() (clusterID string, epoch uint64)
	Store    port.StateStore
	Progress port.ApplyProgress
	Mode     *domain.CellMode
	// Idle diz se o applier está ocioso e a fila dele vazia.
	Idle     func() bool
	IsLeader func() bool
	Tables   []domain.LogicalTable
	Retry    time.Duration // REBUILD_RETRY
	Backoff  Backoff       // erro transitório do banco (AP-07)
	Metrics  port.Metrics
	Log      zerolog.Logger

	mu      sync.Mutex
	stat    RebuildStatus
	refused bool
	again   chan struct{} // um novo POST /admin/reconstruir depois de uma recusa
}

// RebuildStatus é a reconstrução para o /status.
type RebuildStatus struct {
	Tabela        domain.LogicalTable
	ItensCopiados uint64
	F             domain.Index
	Fonte         uint64
	Recusada      string // motivo da recusa (DC-11); vazio = não recusada
}

// Status é o retrato da reconstrução.
func (r *Rebuild) Status() RebuildStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stat
}

// Refused diz se a última tentativa foi recusada (DC-11): o /readyz fica falso.
func (r *Rebuild) Refused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refused
}

// Trigger é o disparo manual (POST /admin/reconstruir). Na líder, ErrLeaderRebuild. Já em
// reconstruindo, não muda nada, a não ser depois de uma recusa: aí tenta de novo.
func (r *Rebuild) Trigger() error {
	if r.IsLeader != nil && r.IsLeader() {
		return ErrLeaderRebuild
	}
	r.mu.Lock()
	if r.refused {
		r.refused, r.stat.Recusada = false, ""
		if r.again != nil {
			close(r.again)
			r.again = nil
		}
	}
	r.mu.Unlock()
	r.Mode.EnterRebuilding()
	return nil
}

func (r *Rebuild) againCh() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.again == nil {
		r.again = make(chan struct{})
	}
	return r.again
}

// Run é o laço de fundo: só age em reconstruindo.
func (r *Rebuild) Run(ctx context.Context) error {
	for {
		ch := r.Mode.Changed()
		if r.Mode.State() != domain.CellRebuilding {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ch:
			}
			continue
		}
		if r.Refused() { // DC-11: não tenta de novo sozinha
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-r.againCh():
			}
			continue
		}
		if err := r.waitIdle(ctx); err != nil {
			return err
		}
		r.Log.Info().Msg("reconstrução iniciada")
		err := r.attempt(ctx)
		switch {
		case err == nil:
			d, err := r.Progress.Sync()
			if err != nil {
				return fmt.Errorf("reconstrução: fsync do progresso: %w", err) // fatal (RT-05)
			}
			r.Log.Info().Uint64("f", uint64(d)).Msgf("reconstrução concluída em F=%d: reiniciando", d)
			return ErrRestartAfterRebuild
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, ErrHistoryMismatch), errors.Is(err, ErrSchemaMismatch):
			r.mu.Lock()
			r.refused, r.stat.Recusada = true, err.Error()
			r.mu.Unlock()
			r.Log.Error().Err(err).Msgf("reconstrução recusada: %s; a sobrescrita no lugar não é segura (DC-11)", err)
		default: // abortada no teto duro, fonte que caiu ou sem fonte: recomeça do início
			r.Log.Warn().Err(err).Dur("espera", r.Retry).Msg("reconstrução interrompida: tentando de novo")
			if err := sleepCtx(ctx, r.Retry); err != nil {
				return err
			}
		}
	}
}

func (r *Rebuild) waitIdle(ctx context.Context) error {
	if r.Idle == nil {
		return nil
	}
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for !r.Idle() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// pickSource escolhe a fonte pelo Retained: em normal, followers primeiro e, entre elas, o
// maior gate.
func (r *Rebuild) pickSource(ctx context.Context) (port.RetainedInfo, error) {
	var cands []port.RetainedInfo
	for _, id := range r.Peers {
		if id == r.Self {
			continue
		}
		info, err := r.Client.Retained(ctx, id)
		if err != nil || info.State != domain.CellNormal.String() {
			continue
		}
		info.Node = id
		cands = append(cands, info)
	}
	if len(cands) == 0 {
		return port.RetainedInfo{}, ErrNoSource
	}
	slices.SortStableFunc(cands, func(a, b port.RetainedInfo) int {
		switch {
		case a.Leader != b.Leader && a.Leader:
			return 1
		case a.Leader != b.Leader:
			return -1
		case a.Gate > b.Gate:
			return -1
		case a.Gate < b.Gate:
			return 1
		}
		return 0
	})
	return cands[0], nil
}

// attempt faz uma cópia inteira. nil = Done e o progresso (F, OrdComplete) gravado.
func (r *Rebuild) attempt(ctx context.Context) error {
	src, err := r.pickSource(ctx)
	if err != nil {
		return err
	}
	if cid, ep := r.Identity(); src.ClusterID != cid || src.Epoch != ep {
		r.Log.Error().Str("fonteGrupo", src.ClusterID).Uint64("fonteEpoch", src.Epoch).Str("localGrupo", cid).
			Uint64("localEpoch", ep).Uint64("fonte", src.Node).Msg("fonte com outra história")
		return fmt.Errorf("%w: fonte %d do grupo %q epoch %d, local %q epoch %d", ErrHistoryMismatch, src.Node,
			src.ClusterID, src.Epoch, cid, ep)
	}
	s, err := r.Client.CopyTable(ctx, src.Node, r.Tables)
	if err != nil {
		return err
	}
	defer s.Close()
	r.mu.Lock()
	r.stat = RebuildStatus{Fonte: src.Node}
	r.mu.Unlock()
	r.Log.Info().Uint64("fonte", src.Node).Msg("reconstrução: copiando da fonte")
	checked := map[domain.LogicalTable]bool{}
	var f domain.Index
	for {
		p, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: fluxo terminou sem Done", port.ErrSourceGone)
		}
		if err != nil {
			return err
		}
		if p.Done {
			if f == 0 {
				f = p.F
			}
			// DC-04: a entrada F inteira está no banco; a partida segue de F + 1.
			if err := r.Progress.SaveProgress(f, port.OrdComplete); err != nil {
				return fmt.Errorf("reconstrução: progresso: %w", err)
			}
			r.mu.Lock()
			r.stat.F = f
			r.mu.Unlock()
			return nil
		}
		if !checked[p.Table] {
			local, ok := r.Catalog.KeySchema(p.Table)
			if !ok || local != p.KeySchema {
				return fmt.Errorf("%w: tabela %s (fonte %v, local %v, presente %v)", ErrSchemaMismatch, p.Table,
					p.KeySchema.Names(), local.Names(), ok)
			}
			checked[p.Table] = true
			if p.F > 0 {
				f = p.F
			}
		}
		for _, it := range p.Items {
			op := []port.VersionedOp{{Op: domain.Op{Table: p.Table, Kind: domain.OpPut, Key: it.Key, Item: it.Item},
				Version: it.Version}}
			if err := r.apply(ctx, op); err != nil {
				return err
			}
		}
		port.MetricsOrNop(r.Metrics).RebuildItems(len(p.Items))
		r.mu.Lock()
		r.stat.Tabela, r.stat.ItensCopiados, r.stat.F = p.Table, r.stat.ItensCopiados+uint64(len(p.Items)), f
		r.mu.Unlock()
	}
}

// apply grava um item pela condição de versão (SG-06), repetindo o erro transitório.
func (r *Rebuild) apply(ctx context.Context, op []port.VersionedOp) error {
	lo, hi := r.Backoff.Min, r.Backoff.Max
	if lo <= 0 {
		lo = 100 * time.Millisecond
	}
	if hi < lo {
		hi = 5 * time.Second
	}
	for wait := lo; ; wait = min(wait*2, hi) {
		err := r.Store.ApplyBatch(ctx, op)
		if err == nil || !errors.Is(err, port.ErrStoreTransient) {
			return err
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
}

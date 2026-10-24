package usecase

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

var (
	// ErrCellRecovering: a célula está fora de normal (recuperando ou reconstruindo) e não
	// responde leitura forte (AP-16). A borda responde 503 CELL_RECOVERING.
	ErrCellRecovering = errors.New("célula em recuperação")
	// ErrNoSource: nenhuma célula retém o que esta célula precisa: só a reconstrução resolve
	// (AP-17, AP-18).
	ErrNoSource = errors.New("nenhuma fonte retém a faixa pedida")
	// ErrCaughtUp: a recuperação já entregou até o alvo.
	ErrCaughtUp = errors.New("recuperação alcançou o alvo")
	// ErrRebuildAborted: a fonte soltou o pino da reconstrução no teto duro (RT-15).
	ErrRebuildAborted = errors.New("reconstrução abortada pela fonte")
	// ErrRestartAfterRebuild: a reconstrução terminou; o processo reinicia (DC-04). É a única
	// saída intencional.
	ErrRestartAfterRebuild = errors.New("reconstrução concluída: reiniciando")
	// ErrHistoryMismatch: a fonte tem outro cluster_id ou epoch; a sobrescrita no lugar não é
	// segura (DC-11).
	ErrHistoryMismatch = errors.New("fonte com outra história")
	// ErrSchemaMismatch: o KeySchema da fonte difere do da tabela local, ou a tabela local
	// não existe (DC-11).
	ErrSchemaMismatch = errors.New("KeySchema da fonte difere do local")
	// ErrLostRaftVolume: o volume do Raft está vazio, mas o grupo já existe (DC-12).
	ErrLostRaftVolume = errors.New("volume do Raft perdido")
)

// Recovery é o canal de recuperação de uma célula atrás da cauda (AP-17, DC-02, DC-03).
// Implementa port.SnapshotPolicy: um MsgSnap à frente do aplicado põe a célula em
// recuperando, e o CommitPump passa a ler as entradas pelo Next, de uma fonte remota
// escolhida pelo Retained. Sem fonte que retenha o que falta, a célula passa a
// reconstruindo (AP-18).
//
// Next só é chamado pela goroutine do CommitPump; o retrato (Progress) é lido pelo /status.
type Recovery struct {
	Self   uint64
	Peers  []uint64
	Client port.RecoveryClient
	Mode   *domain.CellMode
	Gate   *domain.Gate
	// Applier é a marca d'água local (o nome Applied é o método da SnapshotPolicy).
	Applier port.AppliedWaiter
	Retry   time.Duration // REBUILD_RETRY: espera sem nenhuma fonte que responda
	Metrics port.Metrics
	Log     zerolog.Logger

	stream port.EntryStream
	from   domain.Index // próximo índice esperado do stream
	source uint64

	mu   sync.Mutex
	prog RecoveryProgress

	once sync.Once
	logs *rateLog
}

var _ port.SnapshotPolicy = (*Recovery)(nil)

// RecoveryProgress é a recuperação em curso para o /status.
type RecoveryProgress struct {
	Fonte     uint64
	De, Ate   domain.Index
	Alcancado domain.Index
}

// sourceWait é a espera curta depois de uma fonte que caiu ou recusou o fluxo.
const sourceWait = 200 * time.Millisecond

func (r *Recovery) rl() *rateLog {
	r.once.Do(func() { r.logs = newRateLog(10 * time.Second) })
	return r.logs
}

// Applied é a marca d'água do applier: a regra de aceitação do MsgSnap (SG-19).
func (r *Recovery) Applied() domain.Index { return r.Applier.Applied() }

// Behind põe a célula em recuperando com alvo = max(alvo, target). Em reconstruindo,
// ignora (a reconstrução termina adiante do marcador).
func (r *Recovery) Behind(target domain.Index) {
	if !r.Mode.EnterRecovering(target) {
		return
	}
	if ok, sup := r.rl().allow("atras", time.Now()); ok {
		r.Log.Warn().Uint64("alvo", uint64(r.Mode.Target())).Uint64("aplicado", uint64(r.Applier.Applied())).
			Int("suprimidas", sup).Msgf("célula atrás: recuperando até %d", r.Mode.Target())
	}
}

// Progress é o retrato da recuperação em curso.
func (r *Recovery) Progress() RecoveryProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prog
}

// Next devolve o próximo lote a partir de from (DC-03). ErrCaughtUp: já entregou até o alvo.
// ErrNoSource: nenhuma célula retém from. Lote vazio: chamar de novo.
func (r *Recovery) Next(ctx context.Context, from domain.Index) ([]domain.Entry, error) {
	target := r.Mode.Target()
	if from > target {
		r.closeStream()
		return nil, ErrCaughtUp
	}
	if r.stream == nil || from != r.from {
		r.closeStream()
		src, err := r.pickSource(ctx, from)
		if err != nil {
			return nil, err
		}
		s, err := r.Client.CatchUp(ctx, src, from, target)
		if err != nil {
			r.Log.Warn().Err(err).Uint64("fonte", src).Msg("fonte recusou o CatchUp: escolhendo outra")
			return nil, sleepCtx(ctx, sourceWait)
		}
		r.stream, r.from, r.source = s, from, src
		r.Mode.SetSource(strconv.FormatUint(src, 10))
		r.mu.Lock()
		if r.prog.Fonte == 0 || r.prog.Ate < from {
			r.prog.De = from
		}
		r.prog.Fonte, r.prog.Ate = src, target
		r.mu.Unlock()
		r.Log.Info().Uint64("fonte", src).Uint64("de", uint64(from)).Uint64("ate", uint64(target)).Msg("recuperação: fonte escolhida")
	}
	b, err := r.stream.Recv()
	switch {
	case errors.Is(err, io.EOF): // alcançou o To do stream: o pump chama de novo
		r.closeStream()
		return nil, nil
	case errors.Is(err, port.ErrNotRetained):
		r.closeStream()
		return nil, ErrNoSource
	case err != nil: // fonte que caiu ou saiu de normal: escolhe outra
		r.Log.Warn().Err(err).Uint64("fonte", r.source).Msg("fonte da recuperação perdida: trocando de fonte")
		r.closeStream()
		return nil, sleepCtx(ctx, sourceWait)
	}
	if len(b.Entries) == 0 {
		return nil, nil
	}
	if b.Entries[0].Index != from {
		return nil, fmt.Errorf("recuperação: esperava o índice %d, veio %d", from, b.Entries[0].Index)
	}
	last := b.Entries[len(b.Entries)-1].Index
	// DC-02: o gate da follower passa a seguir o confirmado provado pela fonte ANTES de o
	// lote chegar ao applier; sem isso, o commit local parado seguraria o apply.
	r.Gate.SetRecovered(min(last, b.SourceGate))
	r.from = last + 1
	r.mu.Lock()
	r.prog.Alcancado = last
	r.mu.Unlock()
	port.MetricsOrNop(r.Metrics).CatchUpEntries(len(b.Entries))
	return b.Entries, nil
}

func (r *Recovery) closeStream() {
	if r.stream != nil {
		r.stream.Close()
		r.stream = nil
	}
}

// pickSource escolhe a fonte pelo Retained: em normal, com o primeiro retido ≤ from e o
// gate ≥ from; followers primeiro e, entre elas, o maior gate. Nenhuma elegível com uma
// célula normal que já não retém from → ErrNoSource. Nenhuma resposta → espera Retry.
func (r *Recovery) pickSource(ctx context.Context, from domain.Index) (uint64, error) {
	for {
		var cands []port.RetainedInfo
		notRetained := false
		for _, id := range r.Peers {
			if id == r.Self {
				continue
			}
			info, err := r.Client.Retained(ctx, id)
			if err != nil {
				continue
			}
			info.Node = id
			switch {
			case info.State != domain.CellNormal.String():
			case info.FirstRetained > from:
				notRetained = true
			case info.Gate >= from:
				cands = append(cands, info)
			}
		}
		if len(cands) > 0 {
			slices.SortStableFunc(cands, func(a, b port.RetainedInfo) int {
				if a.Leader != b.Leader {
					if a.Leader {
						return 1
					}
					return -1
				}
				return cmp.Compare(b.Gate, a.Gate)
			})
			return cands[0].Node, nil
		}
		if notRetained {
			return 0, ErrNoSource
		}
		if ok, sup := r.rl().allow("sem-fonte", time.Now()); ok {
			r.Log.Warn().Uint64("de", uint64(from)).Int("suprimidas", sup).Msg("recuperação: nenhuma fonte disponível; tentando de novo")
		}
		if err := sleepCtx(ctx, r.Retry); err != nil {
			return 0, err
		}
	}
}

// sleepCtx espera d ou o contexto; devolve só o erro do contexto.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		d = time.Second
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

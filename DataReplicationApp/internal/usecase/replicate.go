package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"

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

	// Modo da célula (AP-16, DC-03): em normal, o log local; em recuperando, a fonte remota
	// (Remote, o Recovery); em reconstruindo, nada. Nulo = sempre normal (testes).
	Mode   *domain.CellMode
	Remote RemoteSource
	// Applied espera o aplicado alcançar o alvo da recuperação antes de voltar a normal.
	Applied port.AppliedWaiter
	Log     zerolog.Logger

	delivered atomic.Uint64 // último índice entregue ao Applier
}

// RemoteSource é a fonte remota das entradas em recuperando (usecase.Recovery).
type RemoteSource interface {
	Next(ctx context.Context, from domain.Index) ([]domain.Entry, error)
}

// readBytes é o teto de cada leitura por faixa do log (RAM ou disco).
const readBytes = 1 << 20

// StartsRebuilding diz se a partida começa em reconstruindo (DC-04, O-42): o progresso do
// applier atrás do primeiro índice retido em disco pularia entradas que o banco da célula
// não tem. Substitui a recusa do Incremento A (a célula não subia). A partida do zero
// (primeiro retido ≤ 2: o índice 1 é o snapshot sintético) nunca reconstrói. Progresso 0
// com o disco já liberado também reconstrói: o disco só é liberado até um progresso com
// fsync (G1), então um progresso ausente quer dizer um volume que perdeu o arquivo.
func StartsRebuilding(progressIdx, firstRetained domain.Index) bool {
	return progressIdx+1 < firstRetained && firstRetained > 2
}

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
	var finishing domain.Index
	for {
		var modeCh <-chan struct{} // nulo sem modo: o select nunca o escolhe
		state := domain.CellNormal
		if p.Mode != nil {
			modeCh = p.Mode.Changed() // antes de ler o estado: não perde transição
			state = p.Mode.State()
		}
		switch state {
		case domain.CellRebuilding: // a reconstrução copia o banco; nada é entregue (AP-18)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-modeCh:
			}
			continue
		case domain.CellRecovering: // a fonte remota (DC-03)
			ents, err := p.Remote.Next(ctx, next)
			switch {
			case errors.Is(err, ErrCaughtUp):
				if t := p.Mode.Target(); t != finishing {
					finishing = t
					go p.finishRecovery(ctx, t)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-modeCh:
				case <-p.Consensus.CommitChanged():
				}
				continue
			case errors.Is(err, ErrNoSource):
				p.Mode.EnterRebuilding()
				p.Log.Warn().Uint64("de", uint64(next)).Msgf("sem fonte que retenha %d: reconstrução", next)
				continue
			case err != nil:
				return err // contexto, ou a fonte quebrou a contiguidade (fatal)
			}
			if len(ents) == 0 {
				continue
			}
			if err := p.deliver(ctx, next, ents); err != nil {
				return err
			}
			next = ents[len(ents)-1].Index + 1
			started = true
			continue
		}
		changed := p.Consensus.CommitChanged() // antes de ler o índice: não perde avanço
		commit := p.Consensus.CommitIndex()
		if next > commit {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			case <-modeCh:
			}
			continue
		}
		ents, err := p.Consensus.CommittedEntries(next, commit, readBytes)
		if errors.Is(err, port.ErrNotRetained) && !started {
			// Na partida, sem a entrada next na RAM nem no disco, a célula está atrás da
			// cauda e vai receber o marcador (O-34, RT-18): espera o commit ou a recuperação.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			case <-modeCh:
			}
			continue
		}
		if err != nil {
			return err // o log perdeu entradas não entregues: bug, fatal (G1)
		}
		if len(ents) == 0 {
			continue
		}
		if err := p.deliver(ctx, next, ents); err != nil {
			return err
		}
		next = ents[len(ents)-1].Index + 1
		started = true
	}
}

// deliver confere a contiguidade e entrega o lote ao applier.
func (p *CommitPump) deliver(ctx context.Context, next domain.Index, ents []domain.Entry) error {
	if ents[0].Index != next {
		return fmt.Errorf("commitpump: esperava o índice %d, veio %d", next, ents[0].Index)
	}
	for i := 1; i < len(ents); i++ {
		if ents[i].Index != ents[i-1].Index+1 {
			return fmt.Errorf("commitpump: buraco depois do índice %d", ents[i-1].Index)
		}
	}
	select {
	case p.ToApplier <- ents: // pode esperar o applier: o laço do Raft não espera
	case <-ctx.Done():
		return ctx.Err()
	}
	p.delivered.Store(uint64(ents[len(ents)-1].Index))
	return nil
}

// finishRecovery espera o aplicado alcançar o alvo e volta a normal, se o alvo não subiu
// nesse meio-tempo (uma goroutine por alvo).
func (p *CommitPump) finishRecovery(ctx context.Context, target domain.Index) {
	if err := p.Applied.WaitApplied(ctx, target); err != nil {
		return
	}
	if p.Mode.SetNormalIf(target) {
		p.Log.Info().Uint64("alvo", uint64(target)).Msg("recuperação concluída")
	}
}

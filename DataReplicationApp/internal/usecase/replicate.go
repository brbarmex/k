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

// ErrStreamClosed: um canal do motor de consenso fechou (o motor parou). É fatal para o
// processo: sem eventos o gate nunca mais avança.
var ErrStreamClosed = errors.New("canal do consenso fechado")

// CommitPump liga o Raft ao resto, em QUATRO goroutines independentes:
//
//  1. progresso: cada resposta de learner da Azure (evento) alimenta o gate no momento
//     em que chega, com termo e identidade (ADR-046), e registra o contato (t_c) que sustenta
//     o lease do modo degradado (ADR-044). Não depende de haver commit novo,
//     nem de o disco do log de replicação ou o applier estarem lentos;
//  2. gate: cada avanço do commit publicado pelo Raft vai direto ao gate (ADR-060). O gate
//     não depende mais da fila do applier;
//  3. commits: lê as entradas commitadas POR FAIXA da memória do Raft, a partir de From,
//     e as entrega (a) ao Applier e (b) à fila do log de replicação;
//  4. log de replicação: grava os lotes da fila, em ordem, sem fsync (ADR-055).
//
// O commit é um sinal, não uma fila (ADR-060): o laço do Raft nunca espera esta bomba.
// Se o applier ou o disco não acompanham, quem espera é a goroutine de commits, e o atraso
// fica nas entradas que o Raft já guarda; a admissão da escrita (APPLY_LAG_MAX) segura a
// entrada de trabalho novo. O Append fica fora da goroutine de commits (ADR-059). Erro de
// escrita no log de replicação continua fatal.
type CommitPump struct {
	Consensus port.ConsensusEngine
	Log       port.ReplicationLog
	Gate      *domain.Gate
	Lease     *domain.Lease // opcional: recebe t_c (último contato com a maioria Azure)
	ToApplier chan<- []domain.Entry
	LogQueue  int // lotes à espera do Append; 0 = defaultLogQueue
	// From é o primeiro índice a entregar (ADR-060, item 5): ResumeFrom na partida. Abaixo
	// do primeiro índice guardado pelo Raft, começa nele.
	From domain.Index
	// ReadBytes é o teto de bytes de cada leitura por faixa; 0 = defaultReadBytes.
	ReadBytes uint64

	delivered atomic.Uint64 // último índice entregue ao Applier
}

// defaultLogQueue é a fila de lotes entre a goroutine de commits e a do Append.
const defaultLogQueue = 1024

// defaultReadBytes é o teto de cada leitura por faixa da memória do Raft.
const defaultReadBytes = 1 << 20

// ResumeFrom é o primeiro índice que a bomba entrega na partida (ADR-060, item 5): o
// menor entre o seguinte ao último do log de replicação e o índice do progresso do
// applier. O log de replicação não tem fsync por entrada, então numa queda de host ele
// pode estar atrás do progresso; começar depois dele abriria um buraco (ErrGap). A
// entrada do progresso é reentregue inteira: o applier pula os itens já gravados pela
// ordem salva, e o replay é idempotente pela versão.
func ResumeFrom(replogLast, progressIdx domain.Index) domain.Index {
	from := replogLast + 1
	if progressIdx > 0 && progressIdx < from {
		from = progressIdx
	}
	if from == 0 {
		from = 1
	}
	return from
}

// Delivered é o último índice entregue ao Applier (métrica dr_commit_undelivered).
func (p *CommitPump) Delivered() domain.Index { return domain.Index(p.delivered.Load()) }

func (p *CommitPump) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	n := p.LogQueue
	if n <= 0 {
		n = defaultLogQueue
	}
	toLog := make(chan []domain.Entry, n)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		if err := p.runProgress(ctx); err != nil {
			cancel(err)
		}
	}()
	go func() {
		defer wg.Done()
		p.runGate(ctx)
	}()
	go func() {
		defer wg.Done()
		if err := p.runAppend(ctx, toLog); err != nil {
			cancel(err)
		}
	}()
	err := p.runCommits(ctx, toLog)
	cancel(err)
	wg.Wait()
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

// runGate leva cada avanço do commit ao gate, sem passar pela fila do applier (ADR-060).
func (p *CommitPump) runGate(ctx context.Context) {
	for {
		changed := p.Consensus.CommitChanged()
		p.Gate.SetTerm(p.Consensus.Term())
		p.Gate.SetRaftCommit(p.Consensus.CommitIndex())
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
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

func (p *CommitPump) runCommits(ctx context.Context, toLog chan<- []domain.Entry) error {
	next := max(p.From, p.Consensus.FirstIndex(), 1)
	p.delivered.Store(uint64(next - 1))
	maxBytes := p.ReadBytes
	if maxBytes == 0 {
		maxBytes = defaultReadBytes
	}
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
		ents, err := p.Consensus.CommittedEntries(next, commit, maxBytes)
		if err != nil {
			return err // a memória do Raft perdeu entradas não entregues: bug, fatal
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
		select {
		case toLog <- ents: // o Append roda na própria goroutine (ADR-059)
		case <-ctx.Done():
			return ctx.Err()
		}
		next = ents[len(ents)-1].Index + 1
		p.delivered.Store(uint64(next - 1))
	}
}

// runAppend grava no log de replicação os lotes da fila, na ordem do commit.
func (p *CommitPump) runAppend(ctx context.Context, toLog <-chan []domain.Entry) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case b := <-toLog:
			if err := p.Log.Append(ctx, b); err != nil {
				return err // erro de escrita no log de replicação é fatal (ADR-055)
			}
		}
	}
}

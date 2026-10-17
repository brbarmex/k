package usecase

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

var (
	// ErrPending: resultado desconhecido; o cliente repete (dedupe e versão absorvem).
	ErrPending = errors.New("resultado desconhecido: o cliente deve repetir")
	// ErrNoLeader: sem líder conhecida depois de WAIT_LEADER (ADR-039). Nada foi proposto.
	ErrNoLeader = errors.New("sem líder conhecida")
	// ErrBackpressure: o buffer de entradas ainda não persistidas do shard está cheio
	// (MaxUncommittedEntriesSize, ADR-056). Nada entrou no log; vira ThrottlingException
	// e o SDK repete com backoff. O adaptador etcdraft traduz raft.ErrProposalDropped.
	ErrBackpressure = errors.New("escrita recusada por contrapressão")
)

// Causas da contrapressão (rótulo `causa` de dr_throttled_total, O-27). Todas viram
// ThrottlingException e em todas nada entrou no log.
const (
	CauseBufferFull = "buffer_cheio" // MaxUncommittedEntriesSize do Raft (ADR-056)
	CauseNotLeader  = "nao_lider"    // a célula deixou de ser líder e ainda não viu a troca
	CauseApplyLag   = "atraso_apply" // gate - aplicado acima de APPLY_LAG_MAX (ADR-060)
)

// BackpressureError é ErrBackpressure com a causa. errors.Is(err, ErrBackpressure) vale.
type BackpressureError struct{ Cause string }

func (e *BackpressureError) Error() string        { return ErrBackpressure.Error() + ": " + e.Cause }
func (e *BackpressureError) Is(target error) bool { return target == ErrBackpressure }

// WriteService é o caso de uso "escrever": acha a líder, propõe, espera o gate, espera
// o apply local e responde. O apply é sempre depois do gate (ADR-057);
// o ACK é sempre depois do gate E do apply. Antes de propor, a líder recusa a escrita se
// o apply local está atrasado demais (admissão, ADR-060).
type WriteService struct {
	Consensus port.ConsensusEngine
	Forward   port.Forwarder
	Gate      *domain.Gate
	Applied   port.AppliedWaiter
	Metrics   port.Metrics // opcional: causa da contrapressão (O-27)

	// MaxApplyLag (APPLY_LAG_MAX) é quantas entradas o apply local pode estar atrás do
	// gate antes de a líder recusar escritas novas com ThrottlingException. 0 = sem
	// admissão (só testes). O ACK espera o apply: uma escrita aceita com o apply muito
	// atrasado só venceria o prazo (PENDING), depois de entrar no log.
	MaxApplyLag domain.Index

	WaitLeader       time.Duration // WAIT_LEADER (1 s); 0 = não espera
	MaxWaitingLeader int           // teto de requisições esperando líder; 0 = sem teto

	waitingLeader atomic.Int64
}

func (s *WriteService) Write(ctx context.Context, r port.WriteRequest) (port.WriteResult, error) {
	leader, err := s.resolveLeader(ctx)
	if err != nil {
		return port.WriteResult{}, err // 503, sem Propose (ADR-039)
	}
	if !leader.Local {
		if r.Forwarded {
			return port.WriteResult{}, ErrNoLeader // já foi encaminhada uma vez: nunca de novo
		}
		r.Forwarded = true
		return s.Forward.Forward(ctx, leader.NodeID, r) // um salto; a líder responde (ADR-010)
	}

	// Admissão por atraso do apply (ADR-060): recusa cedo, sem gastar índice do log.
	if err := s.admit(); err != nil {
		return port.WriteResult{}, err
	}

	e := domain.Entry{Kind: domain.KindWrite, Ops: r.Ops}
	idx, err := s.Consensus.Propose(ctx, e) // commit Raft: 2/3 AWS com fsync
	if err != nil {
		return port.WriteResult{}, err
	}
	if err := s.Gate.WaitFor(ctx, idx); err != nil { // + 2/3 Azure com fsync
		return port.WriteResult{}, ErrPending
	}
	if err := s.Applied.WaitApplied(ctx, idx); err != nil { // apply no banco da célula da líder
		return port.WriteResult{}, ErrPending
	}
	return port.WriteResult{Index: idx}, nil
}

// admit recusa a escrita quando gate - aplicado passa de MaxApplyLag. Mede contra o gate,
// não contra o commit: entradas que esperam a Azure não são atraso do apply.
func (s *WriteService) admit() error {
	if s.MaxApplyLag == 0 {
		return nil
	}
	gate, applied := s.Gate.Index(), s.Applied.Applied()
	if gate > applied && gate-applied > s.MaxApplyLag {
		port.MetricsOrNop(s.Metrics).Throttled(CauseApplyLag)
		return &BackpressureError{Cause: CauseApplyLag}
	}
	return nil
}

// resolveLeader devolve a líder conhecida; sem líder espera até WaitLeader pela eleição
// e depois falha com ErrNoLeader (ADR-039). O teto de esperantes protege memória e
// conexões durante a eleição.
func (s *WriteService) resolveLeader(ctx context.Context) (port.LeaderInfo, error) {
	if l, ok := s.Consensus.Leader(); ok {
		return l, nil
	}
	n := s.waitingLeader.Add(1)
	defer s.waitingLeader.Add(-1)
	if s.MaxWaitingLeader > 0 && n > int64(s.MaxWaitingLeader) {
		return port.LeaderInfo{}, ErrNoLeader
	}
	return waitLeader(ctx, s.Consensus, s.WaitLeader)
}

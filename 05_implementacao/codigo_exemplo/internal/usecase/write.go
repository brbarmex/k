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
	// ErrLeaseExpired: o lease do modo degradado acabou (ADR-044). Alias do domínio.
	ErrLeaseExpired = domain.ErrLeaseExpired
)

// WriteService é o caso de uso "escrever": acha a líder, propõe, espera o gate, espera
// o apply local, confere o lease e responde. O apply é sempre serial e depois do gate
// (ADR-053); o ACK é sempre depois do gate E do apply.
type WriteService struct {
	Consensus port.ConsensusEngine
	Forward   port.Forwarder
	Gate      *domain.Gate
	Applied   port.AppliedWaiter
	Degrade   Degrader // estado real do shard; nil = nunca degradado

	WaitLeader       time.Duration // WAIT_LEADER (1 s); 0 = não espera
	LeaderPoll       time.Duration // intervalo de consulta da líder; padrão 10 ms
	MaxWaitingLeader int           // teto de requisições esperando líder; 0 = sem teto
	DegradeAfter     time.Duration // DEGRADE_AFTER (2 s); 0 = nunca propõe DEGRADE

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

	// Lease vencido: recusa antes de gastar um índice do log.
	if err := s.checkLease(); err != nil {
		return port.WriteResult{}, err
	}

	e := domain.Entry{Kind: domain.KindWrite, Ops: r.Ops, ClientID: r.ClientID}
	idx, err := s.Consensus.Propose(ctx, e) // commit Raft: 2/3 AWS com fsync
	if err != nil {
		return port.WriteResult{}, err
	}
	if err := s.waitGate(ctx, idx); err != nil { // + 2/3 Azure com fsync, ou DEGRADE
		return port.WriteResult{}, ErrPending
	}
	if err := s.Applied.WaitApplied(ctx, idx); err != nil { // apply no banco da célula da líder
		return port.WriteResult{}, ErrPending
	}
	// Conferência a CADA ACK, depois do fsync e do apply e antes de responder (ADR-044):
	// uma pausa de processo durante as esperas acima vence o lease e é pega aqui. A
	// escrita já está commitada: o 503 é "resultado desconhecido" e o retry é absorvido
	// pelo dedupe.
	if err := s.checkLease(); err != nil {
		return port.WriteResult{}, err
	}
	return port.WriteResult{Index: idx, Degraded: s.degraded()}, nil
}

func (s *WriteService) degraded() bool { return s.Degrade != nil && s.Degrade.Active() }

func (s *WriteService) checkLease() error {
	if s.Degrade == nil {
		return nil
	}
	return s.Degrade.CheckLease()
}

// resolveLeader devolve a líder conhecida; sem líder espera até WaitLeader pela eleição
// e depois falha com ErrNoLeader (ADR-039). O teto de esperantes protege memória e
// conexões durante a eleição.
func (s *WriteService) resolveLeader(ctx context.Context) (port.LeaderInfo, error) {
	if l, ok := s.Consensus.Leader(); ok {
		return l, nil
	}
	if s.WaitLeader <= 0 {
		return port.LeaderInfo{}, ErrNoLeader
	}
	n := s.waitingLeader.Add(1)
	defer s.waitingLeader.Add(-1)
	if s.MaxWaitingLeader > 0 && n > int64(s.MaxWaitingLeader) {
		return port.LeaderInfo{}, ErrNoLeader
	}
	poll := s.LeaderPoll
	if poll <= 0 {
		poll = 10 * time.Millisecond
	}
	deadline := time.NewTimer(s.WaitLeader)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return port.LeaderInfo{}, ErrNoLeader
		case <-deadline.C:
			return port.LeaderInfo{}, ErrNoLeader
		case <-tick.C:
			if l, ok := s.Consensus.Leader(); ok {
				return l, nil
			}
		}
	}
}

// waitGate espera o gate. Se a Azure não confirma em DegradeAfter, tenta o DEGRADE;
// com DEGRADED_POLICY=reject o Enter recusa e a espera continua até o prazo da requisição
// (503 PENDING, RPO 0). Falha do Enter (DEGRADE não commitou) também só segue esperando.
func (s *WriteService) waitGate(ctx context.Context, idx domain.Index) error {
	if s.Degrade == nil || s.DegradeAfter <= 0 {
		return s.Gate.WaitFor(ctx, idx)
	}
	gctx, cancel := context.WithTimeout(ctx, s.DegradeAfter)
	err := s.Gate.WaitFor(gctx, idx)
	cancel()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_ = s.Degrade.Enter(ctx) // erro ignorado de propósito: o que vale é o gate abaixo
	return s.Gate.WaitFor(ctx, idx)
}

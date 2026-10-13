package usecase

import (
	"context"
	"errors"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

var ErrPending = errors.New("resultado desconhecido: o cliente deve repetir")

// WriteService é o caso de uso "escrever": propõe, espera o gate, espera o apply
// local e responde. A ordem do apply (serial depois do gate no Strong, paralelo no
// Eventual) é decisão do Applier; aqui o ACK é sempre depois do gate E do apply.
type WriteService struct {
	Consensus port.ConsensusEngine
	Forward   port.Forwarder
	Gate      *domain.Gate
	Applied   port.AppliedWaiter
	Degraded  func() bool
}

func (s *WriteService) Write(ctx context.Context, r port.WriteRequest) (port.WriteResult, error) {
	if leader, ok := s.Consensus.Leader(); ok && !leader.Local {
		return s.Forward.Forward(ctx, leader.NodeID, r) // um salto; a líder responde (ADR-010)
	}

	e := domain.Entry{Kind: domain.KindWrite, Ops: r.Ops, ClientID: r.ClientID}
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
	return port.WriteResult{Index: idx, Degraded: s.Degraded()}, nil
}

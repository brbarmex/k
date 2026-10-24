package usecase

import (
	"context"
	"errors"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

var (
	// ErrNotLeader: a célula local não é a líder (ReadIndex e barreira só na líder).
	ErrNotLeader = errors.New("esta célula não é a líder")
	// ErrReadTimeout: a barreira de leitura ou o apply local não chegaram no prazo.
	ErrReadTimeout = errors.New("leitura forte não alcançou a barreira no prazo")
	// ErrApplierStopped: o applier da célula estacionou num item venenoso antes da barreira;
	// ela nunca será alcançada sem ação humana (ADR-038), então a resposta é imediata (ADR-049).
	ErrApplierStopped = errors.New("applier parado em item venenoso: a barreira não será alcançada sem ação humana")
)

// ReadService é a leitura forte (A.6, ADR-049, ADR-057). Toda leitura é forte e paga uma
// barreira por chamada (cada página do Scan é uma chamada):
//
//   - na líder: ReadIndex (confirma a liderança com o quórum) e o
//     gate lido DEPOIS do ReadIndex;
//   - na follower: pergunta à líder ReadIndex e gate na MESMA resposta (R16); nunca usa
//     um gate guardado;
//   - espera o apply local alcançar min(ReadIndex, gate) e lê a tabela da própria célula.
type ReadService struct {
	Consensus  port.ConsensusEngine
	Gate       *domain.Gate
	Applied    port.AppliedWaiter
	Barrier    port.BarrierClient
	Store      port.StateStore
	Scanner    port.Scanner
	WaitLeader time.Duration // WAIT_LEADER; 0 = não espera
	Metrics    port.Metrics  // duração da barreira (O-16); nulo = sem métricas
	// Mode: fora de normal, a célula não responde leitura forte (AP-16); nulo = normal.
	Mode *domain.CellMode
}

var _ port.Reader = (*ReadService)(nil)

// LeaderBarrier é a barreira servida pela líder: ReadIndex e gate na mesma resposta.
func (s *ReadService) LeaderBarrier(ctx context.Context) (domain.Index, domain.Index, error) {
	if l, ok := s.Consensus.Leader(); !ok || !l.Local {
		return 0, 0, ErrNotLeader
	}
	ri, err := s.Consensus.ReadIndex(ctx)
	if err != nil {
		return 0, 0, err
	}
	return ri, s.Gate.Index(), nil // o gate é lido DEPOIS do ReadIndex
}

// barrier devolve até onde o apply local precisa chegar: min(ReadIndex, gate).
func (s *ReadService) barrier(ctx context.Context) (domain.Index, error) {
	leader, err := waitLeader(ctx, s.Consensus, s.WaitLeader)
	if err != nil {
		return 0, err
	}
	var ri, gate domain.Index
	if leader.Local {
		ri, gate, err = s.LeaderBarrier(ctx)
	} else {
		ri, gate, err = s.Barrier.Barrier(ctx, leader.NodeID)
	}
	if err != nil {
		return 0, err
	}
	return min(ri, gate), nil
}

func (s *ReadService) waitBarrier(ctx context.Context) error {
	if s.Mode != nil && s.Mode.State() != domain.CellNormal {
		return ErrCellRecovering // antes da barreira: o banco local está atrás (AP-16)
	}
	start := time.Now()
	b, err := s.barrier(ctx)
	if err != nil {
		return err
	}
	if err := s.Applied.WaitApplied(ctx, b); err != nil {
		if errors.Is(err, ErrApplierStopped) {
			return ErrApplierStopped // falha rápida, sem esperar o prazo (ADR-049, item 4)
		}
		return ErrReadTimeout
	}
	port.MetricsOrNop(s.Metrics).ReadBarrier(time.Since(start))
	return nil
}

func (s *ReadService) Get(ctx context.Context, table domain.LogicalTable, key []byte) ([]byte, error) {
	if err := s.waitBarrier(ctx); err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, table, key)
}

func (s *ReadService) Scan(ctx context.Context, table domain.LogicalTable, limit int, startKey []byte) (port.ScanPage, error) {
	if err := s.waitBarrier(ctx); err != nil {
		return port.ScanPage{}, err
	}
	return s.Scanner.Scan(ctx, table, limit, startKey)
}

// waitLeader devolve a líder conhecida; sem líder espera até `wait` pela eleição e
// depois falha com ErrNoLeader (ADR-039).
func waitLeader(ctx context.Context, c port.ConsensusEngine, wait time.Duration) (port.LeaderInfo, error) {
	if l, ok := c.Leader(); ok {
		return l, nil
	}
	if wait <= 0 {
		return port.LeaderInfo{}, ErrNoLeader
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return port.LeaderInfo{}, ErrNoLeader
		case <-deadline.C:
			return port.LeaderInfo{}, ErrNoLeader
		case <-tick.C:
			if l, ok := c.Leader(); ok {
				return l, nil
			}
		}
	}
}

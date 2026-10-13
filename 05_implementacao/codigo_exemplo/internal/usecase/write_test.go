package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

type fakeConsensus struct{ idx domain.Index }

func (f fakeConsensus) Propose(context.Context, domain.Entry) (domain.Index, error) {
	return f.idx, nil
}
func (fakeConsensus) ReadIndex(context.Context) (domain.Index, error)  { return 0, nil }
func (fakeConsensus) Leader() (port.LeaderInfo, bool)                  { return port.LeaderInfo{Local: true}, true }
func (fakeConsensus) TransferLeadership(context.Context, string) error { return nil }
func (fakeConsensus) Committed() <-chan port.CommitBatch               { return nil }
func (fakeConsensus) LearnerDurable() [3]domain.Index                  { return [3]domain.Index{} }

type fakeApplied struct{ done chan struct{} }

func (f fakeApplied) WaitApplied(ctx context.Context, _ domain.Index) error {
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newSvc(applied fakeApplied) (*WriteService, *domain.Gate) {
	g := &domain.Gate{}
	return &WriteService{Consensus: fakeConsensus{idx: 5}, Gate: g, Applied: applied, Degraded: func() bool { return false }}, g
}

// O ACK não sai com o gate liberado se o apply local ainda não terminou.
func TestAckWaitsForGateAndApply(t *testing.T) {
	applied := fakeApplied{done: make(chan struct{})}
	svc, g := newSvc(applied)
	g.SetRaftCommit(5)
	g.SetAzureDurable([3]domain.Index{5, 5, 5})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := svc.Write(ctx, port.WriteRequest{}); !errors.Is(err, ErrPending) {
		t.Fatalf("esperado ErrPending sem apply, veio %v", err)
	}

	close(applied.done)
	res, err := svc.Write(context.Background(), port.WriteRequest{})
	if err != nil || res.Index != 5 {
		t.Fatalf("esperado ACK no índice 5, veio %v %v", res, err)
	}
}

// Sem o gate (Azure não confirmou), o ACK também espera, mesmo com o apply pronto.
func TestAckWaitsForAzureGate(t *testing.T) {
	applied := fakeApplied{done: make(chan struct{})}
	close(applied.done)
	svc, g := newSvc(applied)
	g.SetRaftCommit(5)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := svc.Write(ctx, port.WriteRequest{}); !errors.Is(err, ErrPending) {
		t.Fatalf("esperado ErrPending sem gate, veio %v", err)
	}
}

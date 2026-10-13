package bootstrap

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/fx"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

type stubConsensus struct {
	mu   sync.Mutex
	next domain.Index
}

func (s *stubConsensus) Propose(context.Context, domain.Entry) (domain.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return s.next, nil
}
func (*stubConsensus) ReadIndex(context.Context) (domain.Index, error) { return 0, nil }
func (*stubConsensus) Leader() (port.LeaderInfo, bool) {
	return port.LeaderInfo{NodeID: "n1", Local: true}, true
}
func (*stubConsensus) TransferLeadership(context.Context, string) error { return nil }
func (*stubConsensus) Committed() <-chan port.CommitBatch               { return nil }
func (*stubConsensus) Term() domain.Term                                { return 1 }
func (*stubConsensus) LearnerEvents() <-chan port.LearnerProgress       { return nil }
func (*stubConsensus) LearnerDurable() []port.LearnerProgress           { return nil }

type stubForwarder struct{}

func (stubForwarder) Forward(context.Context, string, port.WriteRequest) (port.WriteResult, error) {
	return port.WriteResult{}, nil
}

type stubApplied struct{}

func (stubApplied) WaitApplied(context.Context, domain.Index) error { return nil }

func setEnv(t *testing.T, policy string) {
	t.Setenv("CELL_ID", "a")
	t.Setenv("READ_MODE_DEFAULT", "strong")
	t.Setenv("DEGRADED_POLICY", policy)
	t.Setenv("DEGRADED_LEASE", "5m")
	t.Setenv("DEGRADE_AFTER", "50ms")
}

func build(t *testing.T, policy string) (usecase.Degrader, port.Writer, *domain.Gate, *domain.Lease) {
	t.Helper()
	setEnv(t, policy)
	var (
		deg usecase.Degrader
		w   port.Writer
		g   *domain.Gate
		l   *domain.Lease
	)
	app := New(
		fx.NopLogger,
		fx.Provide(
			func() port.ConsensusEngine { return &stubConsensus{} },
			func() port.Forwarder { return stubForwarder{} },
			func() port.AppliedWaiter { return stubApplied{} },
			// parser mínimo para o grafo fechar; o adaptador de verdade não faz parte do esboço
			func() func(string, []byte) (port.WriteRequest, error) {
				return func(string, []byte) (port.WriteRequest, error) { return port.WriteRequest{}, nil }
			},
		),
		fx.Populate(&deg, &w, &g, &l),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("grafo de dependências inválido: %v", err)
	}
	return deg, w, g, l
}

// O header x-dr-mode vem do estado real do shard: o Degraded do esboço antigo era false fixo.
func TestWriterDegradedFollowsRealShardState(t *testing.T) {
	deg, w, g, lease := build(t, "accept")
	g.SetTerm(1)
	lease.SetTerm(1)
	lease.ObserveContact(1, "az1", time.Now()) // último contato com a maioria Azure (t_c)
	lease.ObserveContact(1, "az2", time.Now())
	g.SetRaftCommit(100)
	g.SetAzureDurable(1, 100, "az1")
	g.SetAzureDurable(1, 100, "az2")

	res, err := w.Write(context.Background(), port.WriteRequest{})
	if err != nil || res.Degraded {
		t.Fatalf("normal: %+v %v", res, err)
	}
	if err := deg.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err = w.Write(context.Background(), port.WriteRequest{})
	if err != nil || !res.Degraded {
		t.Fatalf("degradado: %+v %v", res, err)
	}
}

// reject montado pela configuração: nunca degrada, mesmo com a Azure fora.
func TestWiringRejectNeverDegrades(t *testing.T) {
	deg, w, g, _ := build(t, "reject")
	g.SetTerm(1)
	g.SetRaftCommit(100) // a Azure nunca confirma
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := w.Write(ctx, port.WriteRequest{}); err == nil {
		t.Fatal("sem a Azure o ACK não pode sair com reject")
	}
	if deg.Active() {
		t.Fatal("reject ficou degradado")
	}
}

// A partida recusa configuração incompleta: DEGRADED_POLICY ausente derruba o grafo.
func TestWiringFailsWithoutDegradedPolicy(t *testing.T) {
	setEnv(t, "")
	app := New(fx.NopLogger,
		fx.Provide(
			func() port.ConsensusEngine { return &stubConsensus{} },
			func() port.Forwarder { return stubForwarder{} },
			func() port.AppliedWaiter { return stubApplied{} },
			func() func(string, []byte) (port.WriteRequest, error) {
				return func(string, []byte) (port.WriteRequest, error) { return port.WriteRequest{}, nil }
			},
		))
	if app.Err() == nil {
		t.Fatal("a partida devia falhar sem DEGRADED_POLICY")
	}
}

// Com reject, DEGRADED_LEASE não é exigida (e o grafo fecha sem ela).
func TestWiringRejectDoesNotNeedLease(t *testing.T) {
	setEnv(t, "reject")
	t.Setenv("DEGRADED_LEASE", "")
	app := New(fx.NopLogger,
		fx.Provide(
			func() port.ConsensusEngine { return &stubConsensus{} },
			func() port.Forwarder { return stubForwarder{} },
			func() port.AppliedWaiter { return stubApplied{} },
			func() func(string, []byte) (port.WriteRequest, error) {
				return func(string, []byte) (port.WriteRequest, error) { return port.WriteRequest{}, nil }
			},
		))
	if err := app.Err(); err != nil {
		t.Fatalf("reject sem DEGRADED_LEASE devia montar: %v", err)
	}
}

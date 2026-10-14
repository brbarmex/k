package bootstrap

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/fx"

	"example.com/datareplication/internal/adapter/in/adminhttp"
	"example.com/datareplication/internal/adapter/in/dynamohttp"
	"example.com/datareplication/internal/adapter/out/prommetrics"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/platform/config"
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
func (*stubConsensus) CommitIndex() domain.Index                        { return 0 }
func (*stubConsensus) CommitChanged() <-chan struct{}                   { return nil }
func (*stubConsensus) FirstIndex() domain.Index                         { return 1 }
func (*stubConsensus) CommittedEntries(domain.Index, domain.Index, uint64) ([]domain.Entry, error) {
	return nil, nil
}
func (*stubConsensus) Term() domain.Term                          { return 1 }
func (*stubConsensus) LearnerEvents() <-chan port.LearnerProgress { return nil }
func (*stubConsensus) LearnerDurable() []port.LearnerProgress     { return nil }

type stubForwarder struct{}

func (stubForwarder) Forward(context.Context, string, port.WriteRequest) (port.WriteResult, error) {
	return port.WriteResult{}, nil
}

type stubReader struct{}

func (stubReader) Get(context.Context, domain.LogicalTable, []byte) ([]byte, error) { return nil, nil }
func (stubReader) Scan(context.Context, domain.LogicalTable, int, []byte) (port.ScanPage, error) {
	return port.ScanPage{}, nil
}

type stubApplied struct{}

func (stubApplied) WaitApplied(context.Context, domain.Index) error { return nil }
func (stubApplied) Applied() domain.Index                           { return 0 }

func setEnv(t *testing.T, policy string) {
	t.Setenv("CELL_ID", "a")
	t.Setenv("DEGRADED_POLICY", policy)
	t.Setenv("DEGRADED_LEASE", "5m")
	t.Setenv("DEGRADE_AFTER", "50ms")
	t.Setenv("NODE_ID", "1")
	t.Setenv("PEERS", "1=cell-1:7000,2=cell-2:7000,3=cell-3:7000")
	t.Setenv("LEARNERS", "4=az-1:7000,5=az-2:7000,6=az-3:7000") // gate com Azure, como em produção
	t.Setenv("TABLES", "cadastro_cliente_cell_{cell}")
	t.Setenv("AWS_REGION", "sa-east-1")
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
			func() port.Reader { return stubReader{} },
			// parser mínimo para o grafo fechar
			func() dynamohttp.ParseFunc {
				return func(string, []byte) (dynamohttp.Request, error) { return dynamohttp.Request{}, nil }
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
			func() port.Reader { return stubReader{} },
			func() dynamohttp.ParseFunc {
				return func(string, []byte) (dynamohttp.Request, error) { return dynamohttp.Request{}, nil }
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
			func() port.Reader { return stubReader{} },
			func() dynamohttp.ParseFunc {
				return func(string, []byte) (dynamohttp.Request, error) { return dynamohttp.Request{}, nil }
			},
		))
	if err := app.Err(); err != nil {
		t.Fatalf("reject sem DEGRADED_LEASE devia montar: %v", err)
	}
}

// O grafo completo de produção fecha (O-09): todos os provedores e invocações casam.
func TestProductionGraphIsValid(t *testing.T) {
	setEnv(t, "reject")
	t.Setenv("LEARNERS", "")
	t.Setenv("POC_SEM_AZURE", "true")
	if err := fx.ValidateApp(fx.NopLogger, Runtime(), fx.Provide(
		config.Load, newLogger, newGate,
		func() *domain.Lease { return &domain.Lease{} },
		func() domain.Clock { return time.Now },
		fx.Annotate(newDegradeService, fx.As(new(usecase.Degrader))),
		fx.Annotate(newWriteService, fx.As(new(port.Writer))),
		newHTTPOptions,
		dynamohttp.New,
	), fx.Invoke(runHTTP)); err != nil {
		t.Fatalf("grafo de produção inválido: %v", err)
	}
}

// Sem learners e sem POC_SEM_AZURE a partida falha (SG-12).
func TestEmptyLearnersNeedsPOCFlag(t *testing.T) {
	setEnv(t, "reject")
	t.Setenv("LEARNERS", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("LEARNERS vazia sem POC_SEM_AZURE devia falhar")
	}
	t.Setenv("POC_SEM_AZURE", "true")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if g := newGate(cfg, zerolog.Nop()); !g.Snapshot().AzureOff || !g.Ready() {
		t.Fatal("sem learners o gate devia estar no modo sem Azure")
	}
}

// O-17: o /metrics de produção expõe o estado lido do StatusReporter.
func TestMetricsEndpointEndToEnd(t *testing.T) {
	rec := prommetrics.NewRecorder("1", nil)
	rec.RegisterState(fakeStatusSource{})
	h := adminhttp.New(":0", fakeStatusSource{}, rec.Handler()).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `dr_is_leader{celula="1"} 1`) {
		t.Fatalf("/metrics %d:\n%s", w.Code, w.Body.String())
	}
}

type fakeStatusSource struct{}

func (fakeStatusSource) Status() usecase.CellStatus {
	return usecase.CellStatus{Celula: "1", No: 1, Papel: "lider", Lider: 1}
}
func (fakeStatusSource) Ready() (bool, string) { return true, "" }

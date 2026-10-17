package bootstrap

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/fx"

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
func (*stubConsensus) CommitIndex() domain.Index      { return 0 }
func (*stubConsensus) CommitChanged() <-chan struct{} { return nil }
func (*stubConsensus) FirstIndex() domain.Index       { return 1 }
func (*stubConsensus) CommittedEntries(domain.Index, domain.Index, uint64) ([]domain.Entry, error) {
	return nil, nil
}
func (*stubConsensus) Term() domain.Term { return 1 }

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

func setEnv(t *testing.T) {
	t.Setenv("CELL_ID", "a")
	t.Setenv("NODE_ID", "1")
	t.Setenv("PEERS", "1=cell-1:7000,2=cell-2:7000,3=cell-3:7000")
	t.Setenv("TABLES", "cadastro_cliente_cell_{cell}")
	t.Setenv("AWS_REGION", "sa-east-1")
}

func build(t *testing.T) (port.Writer, *domain.Gate) {
	t.Helper()
	setEnv(t)
	var (
		w port.Writer
		g *domain.Gate
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
		fx.Populate(&w, &g),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("grafo de dependências inválido: %v", err)
	}
	return w, g
}

// O grafo do núcleo fecha e a escrita ACKa quando o gate libera.
func TestWriterWiring(t *testing.T) {
	w, g := build(t)
	g.SetRaftCommit(100)
	if _, err := w.Write(context.Background(), port.WriteRequest{}); err != nil {
		t.Fatal(err)
	}
}

// O grafo completo de produção fecha (O-09): todos os provedores e invocações casam.
func TestProductionGraphIsValid(t *testing.T) {
	setEnv(t)
	if err := fx.ValidateApp(fx.NopLogger, Runtime(), fx.Provide(
		config.Load, newLogger, newGate,
		fx.Annotate(newWriteService, fx.As(new(port.Writer))),
		newHTTPOptions,
		dynamohttp.New,
	), fx.Invoke(runHTTP)); err != nil {
		t.Fatalf("grafo de produção inválido: %v", err)
	}
}

// O-17: o /metrics de produção expõe o estado lido do StatusReporter.
func TestMetricsEndpointEndToEnd(t *testing.T) {
	rec := prommetrics.NewRecorder("1", nil)
	rec.RegisterState(fakeStatusSource{})
	h := rec.Handler() // a rota /metrics do adminhttp é coberta no teste dele
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

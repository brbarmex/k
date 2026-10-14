// Package bootstrap liga adaptadores às portas. É o ÚNICO lugar que importa fx.
package bootstrap

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/fx"

	"example.com/datareplication/internal/adapter/in/adminhttp"
	"example.com/datareplication/internal/adapter/in/dynamohttp"
	"example.com/datareplication/internal/adapter/in/peergrpc"
	"example.com/datareplication/internal/adapter/out/dynamodb"
	"example.com/datareplication/internal/adapter/out/etcdraft"
	"example.com/datareplication/internal/adapter/out/peerclient"
	"example.com/datareplication/internal/adapter/out/replog"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/platform/config"
	"example.com/datareplication/internal/platform/logx"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// maxWaitingLeader é o teto de requisições esperando uma eleição (ADR-039, consequências).
const maxWaitingLeader = 1000

// replogSegment é o tamanho de cada segmento do log de replicação.
const replogSegment = 64 << 20

// applierQueue é a fila em memória entre o CommitPump e o Applier (risco 3: generosa na POC).
const applierQueue = 4096

// New monta o núcleo (configuração, gate, escrita, borda HTTP). Os adaptadores de Raft,
// banco e transporte entram por `extra` (Runtime em produção, substitutos nos testes).
func New(extra ...fx.Option) *fx.App {
	return fx.New(
		fx.StartTimeout(5*time.Minute), // espera pelas tabelas (TABLE_WAIT) e recuperação do WAL
		fx.StopTimeout(25*time.Second), // < terminationGracePeriodSeconds do pod
		fx.Provide(
			config.Load,
			newLogger,
			newGate,
			func() *domain.Lease { return &domain.Lease{} },
			func() domain.Clock { return time.Now }, // monotônico; os testes injetam outro
			fx.Annotate(newDegradeService, fx.As(new(usecase.Degrader))),
			fx.Annotate(newWriteService, fx.As(new(port.Writer))),
			func(c config.Config) dynamohttp.Options {
				return dynamohttp.Options{Deadline: dynamohttp.Deadline(c.RequestDeadline), IdleTimeout: c.IdleTimeout}
			},
			dynamohttp.New,
		),
		fx.Options(extra...),
		fx.Invoke(runHTTP), // a API abre por último: depende de tudo acima (ordem de OnStart)
	)
}

// NewProduction é o processo completo.
func NewProduction() *fx.App { return New(fx.NopLogger, Runtime()) }

func newLogger(cfg config.Config) zerolog.Logger {
	return logx.New().With().Str("celula", cfg.CellID).Uint64("no", cfg.NodeID).Logger()
}

// newGate liga o modo sem Azure quando LEARNERS está vazia; config.Load só aceita isso
// com POC_SEM_AZURE=true (SG-12).
func newGate(cfg config.Config, log zerolog.Logger) *domain.Gate {
	g := &domain.Gate{}
	if len(cfg.Learners) == 0 {
		// SÓ DA POC (G-08): sem learners o gate é o commit do Raft. Nunca em produção.
		g.SetAzureOff(true)
		log.Warn().Msg("POC sem Azure: gate = commit do Raft; nunca usar em produção")
	}
	return g
}

// newDegradeService liga a política e o lease da configuração ao estado do shard.
// DEGRADED_POLICY e DEGRADED_LEASE já foram validadas por config.Load.
func newDegradeService(cfg config.Config, c port.ConsensusEngine, g *domain.Gate, l *domain.Lease, clk domain.Clock) (*usecase.DegradeService, error) {
	policy, err := domain.ParseDegradedPolicy(cfg.DegradedPolicy)
	if err != nil {
		return nil, err
	}
	return &usecase.DegradeService{Policy: policy, LeaseDuration: cfg.DegradedLease, Consensus: c, Gate: g, Lease: l, Clock: clk}, nil
}

// newWriteService: o header x-dr-mode vem do estado REAL do shard (o Degrader consulta o
// lease), não de uma função fixa.
func newWriteService(cfg config.Config, c port.ConsensusEngine, f port.Forwarder, a port.AppliedWaiter, g *domain.Gate, d usecase.Degrader) *usecase.WriteService {
	return &usecase.WriteService{
		Consensus: c, Forward: f, Gate: g, Applied: a, Degrade: d,
		WaitLeader: cfg.WaitLeader, DegradeAfter: cfg.DegradeAfter, MaxWaitingLeader: maxWaitingLeader,
	}
}

// runHTTP abre a porta de API só depois que as dependências estão prontas (ordem de OnStart).
func runHTTP(lc fx.Lifecycle, s *dynamohttp.Server, cfg config.Config, log zerolog.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				if err := s.ListenAndServe(cfg.APIAddr); err != nil {
					log.Fatal().Err(err).Msg("api caiu")
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error { return s.Shutdown(ctx) },
	})
}

// Runtime liga os adaptadores reais: log de replicação, DynamoDB, gRPC entre células,
// Raft, laços de fundo e administração.
func Runtime() fx.Option {
	return fx.Options(
		fx.Provide(
			func(cfg config.Config) (domain.TableMap, error) { return domain.NewTableMap(cfg.CellID, cfg.Tables) },
			newReplog,
			func(l *replog.Log) port.ReplicationLog { return l },
			func(l *replog.Log) port.ApplyProgress { return l },
			newStore,
			func(s *dynamodb.Store) port.StateStore { return s },
			func(s *dynamodb.Store) port.Scanner { return s },
			func(s *dynamodb.Store) port.Catalog { return s },
			newPeerClient,
			func(c *peerclient.Client) port.Forwarder { return c },
			func(c *peerclient.Client) port.BarrierClient { return c },
			func(c *peerclient.Client) port.GatePublisher { return c },
			newRaft,
			func(n *etcdraft.Node) port.ConsensusEngine { return n },
			func(n *etcdraft.Node) port.RaftCompactor { return n },
			func(n *etcdraft.Node) port.RaftInbound { return n },
			func(n *etcdraft.Node) port.RaftStatus { return n },
			func() chan []domain.Entry { return make(chan []domain.Entry, applierQueue) },
			newApplier,
			func(a *usecase.Applier) port.AppliedWaiter { return a },
			newReadService,
			func(r *usecase.ReadService) port.Reader { return r },
			newGateSync,
			newStatus,
			func(tm domain.TableMap, cat port.Catalog) dynamohttp.ParseFunc {
				return (&dynamohttp.Parser{Tables: tm, Catalog: cat}).Parse
			},
		),
		fx.Invoke(runAdmin, runRuntime),
	)
}

func newReplog(lc fx.Lifecycle, cfg config.Config) (*replog.Log, error) {
	l, err := replog.Open(cfg.ReplogDir, replogSegment)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.StopHook(l.Close))
	return l, nil
}

func newStore(cfg config.Config, tm domain.TableMap) (*dynamodb.Store, error) {
	return dynamodb.New(context.Background(), cfg.DynamoEndpoint, cfg.AWSRegion, tm)
}

func newPeerClient(lc fx.Lifecycle, cfg config.Config, log zerolog.Logger) (*peerclient.Client, error) {
	members := make(map[uint64]string, len(cfg.Peers)+len(cfg.Learners))
	for id, a := range cfg.Peers {
		members[id] = a
	}
	for id, a := range cfg.Learners {
		members[id] = a
	}
	c, err := peerclient.New(cfg.NodeID, members, cfg.ForwardTimeout, log)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.StopHook(c.Close))
	return c, nil
}

func newRaft(lc fx.Lifecycle, cfg config.Config, send *peerclient.Client, log zerolog.Logger) (*etcdraft.Node, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.RaftDir), 0o755); err != nil {
		return nil, err
	}
	n, err := etcdraft.Open(etcdraft.Config{
		ID: cfg.NodeID, Voters: config.IDs(cfg.Peers), Learners: config.IDs(cfg.Learners),
		Dir: cfg.RaftDir, TickInterval: 100 * time.Millisecond,
	}, send, log)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.StopHook(n.Close))
	return n, nil
}

func newApplier(g *domain.Gate, st port.StateStore, p port.ApplyProgress, in chan []domain.Entry, log zerolog.Logger) *usecase.Applier {
	return &usecase.Applier{Gate: g, Store: st, Progress: p, In: in,
		Backoff: usecase.Backoff{Min: 100 * time.Millisecond, Max: 5 * time.Second}, Log: log}
}

func newReadService(cfg config.Config, c port.ConsensusEngine, g *domain.Gate, a port.AppliedWaiter, b port.BarrierClient, st port.StateStore, sc port.Scanner) *usecase.ReadService {
	return &usecase.ReadService{Consensus: c, Gate: g, Applied: a, Barrier: b, Store: st, Scanner: sc, WaitLeader: cfg.WaitLeader}
}

func newGateSync(cfg config.Config, c port.ConsensusEngine, g *domain.Gate, p port.GatePublisher, log zerolog.Logger) *usecase.GateSync {
	return &usecase.GateSync{Consensus: c, Gate: g, Publisher: p, Interval: cfg.GatePublishInterval, Log: log}
}

func newStatus(cfg config.Config, r port.RaftStatus, g *domain.Gate, a *usecase.Applier, l port.ReplicationLog, cat port.Catalog) *usecase.StatusReporter {
	return &usecase.StatusReporter{CellID: cfg.CellID, NodeID: cfg.NodeID, Raft: r, Gate: g, Applier: a, Log: l, Catalog: cat, AzureOff: len(cfg.Learners) == 0}
}

// runAdmin abre a administração primeiro: o /livez responde enquanto as tabelas são
// conferidas.
func runAdmin(lc fx.Lifecycle, cfg config.Config, st *usecase.StatusReporter, log zerolog.Logger) {
	s := adminhttp.New(cfg.AdminAddr, st)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				if err := s.ListenAndServe(); err != nil {
					log.Fatal().Err(err).Msg("administração caiu")
				}
			}()
			return nil
		},
		OnStop: s.Shutdown,
	})
}

type runtimeDeps struct {
	fx.In
	Lc       fx.Lifecycle
	Cfg      config.Config
	Log      zerolog.Logger
	Store    *dynamodb.Store
	Node     *etcdraft.Node
	Gate     *domain.Gate
	Lease    *domain.Lease
	Replog   port.ReplicationLog
	Applier  *usecase.Applier
	ToApply  chan []domain.Entry
	GateSync *usecase.GateSync
	Reads    *usecase.ReadService
	Writer   port.Writer
}

// runRuntime confere as tabelas, abre o gRPC entre células e roda os laços de fundo.
// Erro de qualquer laço (fsync, log de replicação, canal fechado) derruba o processo e o
// Docker reinicia (RT-05). O Applier estacionado não é erro: o processo fica de pé.
func runRuntime(d runtimeDeps) {
	ctx, cancel := context.WithCancel(context.Background())
	peer := &peergrpc.Server{Raft: d.Node, Writer: d.Writer, Barrier: d.Reads, Gate: d.GateSync}
	var wg sync.WaitGroup
	fatal := func(name string, run func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				d.Log.Fatal().Err(err).Str("laco", name).Msg("laço de fundo parou")
			}
		}()
	}
	d.Lc.Append(fx.Hook{
		OnStart: func(startCtx context.Context) error {
			if err := d.Store.LoadCatalog(startCtx, d.Cfg.TableWait, d.Log); err != nil {
				return err
			}
			lis, err := net.Listen("tcp", d.Cfg.PeerAddr)
			if err != nil {
				return err
			}
			go func() {
				if err := peer.Serve(lis); err != nil {
					d.Log.Fatal().Err(err).Msg("gRPC entre células caiu")
				}
			}()
			pump := &usecase.CommitPump{Consensus: d.Node, Log: d.Replog, Gate: d.Gate, Lease: d.Lease, ToApplier: d.ToApply}
			ckpt := &usecase.Checkpointer{Log: d.Replog, Raft: d.Node, Interval: d.Cfg.ReplogSyncInterval}
			fatal("raft", d.Node.Run)
			fatal("commitpump", pump.Run)
			fatal("applier", d.Applier.Run)
			fatal("checkpoint", ckpt.Run)
			fatal("gatesync", d.GateSync.Run)
			d.Log.Info().Str("peer", d.Cfg.PeerAddr).Msg("célula iniciada")
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			peer.Stop()
			cancel()
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select { // os laços param antes de fechar o WAL e o log de replicação
			case <-done:
				return nil
			case <-stopCtx.Done():
				return stopCtx.Err()
			}
		},
	})
}

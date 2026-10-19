// Package bootstrap liga adaptadores às portas. É o ÚNICO lugar que importa fx.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
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
	"example.com/datareplication/internal/adapter/out/progress"
	"example.com/datareplication/internal/adapter/out/prommetrics"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/platform/config"
	"example.com/datareplication/internal/platform/logx"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// maxWaitingLeader é o teto de requisições esperando uma eleição (ADR-039, consequências).
const maxWaitingLeader = 1000

// applierQueue é a fila em memória entre o CommitPump e o Applier, em lotes lidos por faixa
// (até 1 MiB cada). Cheia, quem espera é o CommitPump, nunca o laço do Raft (ADR-060).
const applierQueue = 64

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
			fx.Annotate(newWriteService, fx.As(new(port.Writer))),
			newHTTPOptions,
			dynamohttp.New,
		),
		fx.Options(extra...),
		fx.Invoke(runHTTP), // a API abre por último: depende de tudo acima (ordem de OnStart)
	)
}

// NewProduction é o processo completo.
func NewProduction() *fx.App { return New(fx.NopLogger, Runtime()) }

// httpOptionsIn: as métricas são opcionais no núcleo; o Runtime as provê (O-17).
type httpOptionsIn struct {
	fx.In
	Cfg     config.Config
	Metrics port.Metrics `optional:"true"`
}

func newHTTPOptions(in httpOptionsIn) dynamohttp.Options {
	return dynamohttp.Options{Deadline: dynamohttp.Deadline(in.Cfg.RequestDeadline), IdleTimeout: in.Cfg.IdleTimeout,
		Metrics: port.MetricsOrNop(in.Metrics)}
}

func newLogger(cfg config.Config) zerolog.Logger {
	return logx.New().With().Str("celula", cfg.CellID).Uint64("no", cfg.NodeID).Logger()
}

func newGate() *domain.Gate { return &domain.Gate{} }

// writeServiceIn: as métricas são opcionais no núcleo; o Runtime as provê (O-27).
type writeServiceIn struct {
	fx.In
	Cfg     config.Config
	C       port.ConsensusEngine
	F       port.Forwarder
	A       port.AppliedWaiter
	G       *domain.Gate
	Metrics port.Metrics `optional:"true"`
}

// newWriteService: a admissão por atraso do apply usa APPLY_LAG_MAX (ADR-060).
func newWriteService(in writeServiceIn) *usecase.WriteService {
	return &usecase.WriteService{
		Consensus: in.C, Forward: in.F, Gate: in.G, Applied: in.A, Metrics: in.Metrics,
		MaxApplyLag: domain.Index(in.Cfg.ApplyLagMax),
		WaitLeader:  in.Cfg.WaitLeader, MaxWaitingLeader: maxWaitingLeader,
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

// Runtime liga os adaptadores reais: progresso do applier, DynamoDB, gRPC entre células,
// Raft, laços de fundo e administração.
func Runtime() fx.Option {
	return fx.Options(
		fx.Provide(
			func(cfg config.Config) *prommetrics.Recorder { return prommetrics.NewRecorder(cfg.CellID, nil) },
			func(r *prommetrics.Recorder) port.Metrics { return r },
			func(cfg config.Config) (domain.TableMap, error) { return domain.NewTableMap(cfg.CellID, cfg.Tables) },
			newProgress,
			func(p *progress.File) port.ApplyProgress { return p },
			newStore,
			func(s CellStore) port.StateStore { return s },
			func(s CellStore) port.Scanner { return s },
			func(s CellStore) port.Catalog { return s },
			newResume,
			newPeerClient,
			func(c *peerclient.Client) port.Forwarder { return c },
			func(c *peerclient.Client) port.BarrierClient { return c },
			func(c *peerclient.Client) port.GatePublisher { return c },
			newRaft,
			func(n *etcdraft.Node) port.ConsensusEngine { return n },
			func(n *etcdraft.Node) port.RaftInbound { return n },
			func(n *etcdraft.Node) port.RaftStatus { return n },
			func() chan []domain.Entry { return make(chan []domain.Entry, applierQueue) },
			newApplier,
			func(a *usecase.Applier) port.AppliedWaiter { return a },
			newCommitPump,
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

// newProgress abre o progresso do applier no volume do Raft, ao lado do diretório do WAL
// (o wal.Create renomeia RAFT_DIR, então o arquivo fica no diretório pai).
func newProgress(lc fx.Lifecycle, cfg config.Config) (*progress.File, error) {
	p, err := progress.Open(filepath.Join(filepath.Dir(cfg.RaftDir), "progress"))
	if err != nil {
		return nil, err
	}
	lc.Append(fx.StopHook(p.Close))
	return p, nil
}

// CellStore é o banco da célula com o catálogo conferido na partida (DynamoDB).
type CellStore interface {
	port.StateStore
	port.Scanner
	port.Catalog
	LoadCatalog(ctx context.Context, wait time.Duration, log zerolog.Logger) error
}

func newStore(cfg config.Config, tm domain.TableMap) (CellStore, error) {
	return dynamodb.New(context.Background(), cfg.DynamoEndpoint, cfg.AWSRegion, tm, cfg.ApplyWorkers)
}

// Resume é onde a partida retoma a entrega das entradas commitadas (ADR-060, item 5).
type Resume struct {
	From domain.Index // primeiro índice que o CommitPump entrega
}

// newResume calcula o ponto de retomada a partir do progresso do applier, para o Raft não
// reentregar o log inteiro a cada partida.
func newResume(p port.ApplyProgress) (Resume, error) {
	idx, _, err := p.LoadProgress()
	if err != nil {
		return Resume{}, err
	}
	return Resume{From: usecase.ResumeFrom(idx)}, nil
}

func newPeerClient(lc fx.Lifecycle, cfg config.Config, log zerolog.Logger, m port.Metrics) (*peerclient.Client, error) {
	c, err := peerclient.New(cfg.NodeID, cfg.Peers, cfg.ForwardTimeout, cfg.PeerQueueBytes, log)
	if err != nil {
		return nil, err
	}
	c.Metrics = m
	lc.Append(fx.StopHook(c.Close))
	return c, nil
}

// raftIn reúne o que o nó Raft precisa: o transporte, o progresso e o gate (Compactor) e o
// aplicado (política do MsgSnap).
type raftIn struct {
	fx.In
	Lc       fx.Lifecycle
	Cfg      config.Config
	Send     *peerclient.Client
	Log      zerolog.Logger
	Metrics  port.Metrics
	Resume   Resume
	Progress port.ApplyProgress
	Gate     *domain.Gate
	Applied  port.AppliedWaiter
}

func newRaft(in raftIn) (*etcdraft.Node, error) {
	cfg := in.Cfg
	volume := filepath.Dir(cfg.RaftDir)
	if err := os.MkdirAll(volume, 0o755); err != nil {
		return nil, err
	}
	// RETAIN_MAX_BYTES < volume × RETAIN_DISK_HARD (O-37): a janela precisa caber abaixo do teto
	// duro, senão a réplica atrasada nunca sai da janela antes de o disco encher.
	if vb, err := etcdraft.VolumeBytes(volume); err == nil && float64(cfg.RetainMaxBytes) >= float64(vb)*cfg.RetainDiskHard {
		return nil, fmt.Errorf("RETAIN_MAX_BYTES maior que o teto duro do volume (%d >= %d × %.2f)", cfg.RetainMaxBytes, vb, cfg.RetainDiskHard)
	}
	n, err := etcdraft.Open(etcdraft.Config{
		ID: cfg.NodeID, Voters: config.IDs(cfg.Peers),
		Dir: cfg.RaftDir, TickInterval: 100 * time.Millisecond, ElectionTicks: cfg.ElectionTicks,
		Applied: uint64(in.Resume.From) - 1, Metrics: in.Metrics,
		ClusterID: cfg.ClusterID, SegmentBytes: cfg.WalSegmentBytes,
		MarkerPath: filepath.Join(volume, "snapshot"),
		Policy:     newNoopPolicy(in.Applied, in.Log),
		Compaction: etcdraft.CompactionConfig{
			Progress: in.Progress, Gate: in.Gate,
			EveryEntries: cfg.SnapshotEveryEntries, EveryBytes: cfg.SnapshotEveryBytes,
			CatchupEntries: cfg.RaftCatchupEntries, RAMMax: cfg.RaftRAMMax,
			RetainMax: cfg.RetainMaxBytes, HardFrac: cfg.RetainDiskHard, HardBytes: cfg.RetainDiskHardBytes,
			Interval: cfg.RaftCompactInterval,
		},
	}, in.Send, in.Log)
	if err != nil {
		return nil, err
	}
	// G6: o confirmado nunca recua depois de um reinício; o do marcador restaura o gate antes
	// de os laços começarem.
	in.Gate.Restore(n.Marker().Confirmed)
	in.Lc.Append(fx.StopHook(n.Close))
	return n, nil
}

// noopPolicy é a política do MsgSnap do Incremento A (O-37): aceita o marcador só com o
// aplicado ≥ S (SG-19) e, atrás da cauda, só avisa. A recuperação pelo canal entre células
// é do Incremento B; até lá a célula fica parada e visível no /status (atrasCauda).
type noopPolicy struct {
	applied port.AppliedWaiter
	log     zerolog.Logger

	mu   sync.Mutex
	last time.Time
}

func newNoopPolicy(a port.AppliedWaiter, log zerolog.Logger) *noopPolicy {
	return &noopPolicy{applied: a, log: log}
}

func (p *noopPolicy) Applied() domain.Index { return p.applied.Applied() }

func (p *noopPolicy) Behind(target domain.Index) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now := time.Now(); now.Sub(p.last) >= 10*time.Second {
		p.last = now
		p.log.Warn().Uint64("alvo", uint64(target)).Uint64("aplicado", uint64(p.applied.Applied())).
			Msg("célula atrás da cauda: aguardando recuperação (Incremento B)")
	}
}

func newApplier(cfg config.Config, g *domain.Gate, st port.StateStore, p port.ApplyProgress, in chan []domain.Entry, log zerolog.Logger, m port.Metrics) *usecase.Applier {
	return &usecase.Applier{Gate: g, Store: st, Progress: p, In: in, Workers: cfg.ApplyWorkers,
		Backoff: usecase.Backoff{Min: 100 * time.Millisecond, Max: 5 * time.Second}, Log: log, Metrics: m}
}

func newReadService(cfg config.Config, c port.ConsensusEngine, g *domain.Gate, a port.AppliedWaiter, b port.BarrierClient, st port.StateStore, sc port.Scanner, m port.Metrics) *usecase.ReadService {
	return &usecase.ReadService{Consensus: c, Gate: g, Applied: a, Barrier: b, Store: st, Scanner: sc, WaitLeader: cfg.WaitLeader, Metrics: m}
}

// newGateSync: o piso de retenção vai junto com o gate (RT-15). Na líder, o Compactor o
// calcula; na follower, a retenção local recebe o da líder.
func newGateSync(cfg config.Config, c port.ConsensusEngine, g *domain.Gate, p port.GatePublisher, log zerolog.Logger, n *etcdraft.Node) *usecase.GateSync {
	return &usecase.GateSync{Consensus: c, Gate: g, Publisher: p, Interval: cfg.GatePublishInterval, Log: log,
		Floor: n.Floor, FloorSink: n.Pins()}
}

func newCommitPump(n *etcdraft.Node, g *domain.Gate, to chan []domain.Entry, r Resume) *usecase.CommitPump {
	return &usecase.CommitPump{Consensus: n, Gate: g, ToApplier: to, From: r.From}
}

func newStatus(cfg config.Config, r port.RaftStatus, g *domain.Gate, a *usecase.Applier, cat port.Catalog,
	p *usecase.CommitPump, inbox chan []domain.Entry) *usecase.StatusReporter {
	return &usecase.StatusReporter{CellID: cfg.CellID, NodeID: cfg.NodeID, Raft: r, Gate: g, Applier: a, Catalog: cat,
		Pump: p, ApplyInbox: func() int { return len(inbox) },
		ApplyLagMax: cfg.ApplyLagMax, ElectionTicks: cfg.ElectionTicks}
}

// runAdmin abre a administração primeiro: o /livez responde enquanto as tabelas são
// conferidas.
func runAdmin(lc fx.Lifecycle, cfg config.Config, st *usecase.StatusReporter, rec *prommetrics.Recorder, log zerolog.Logger) {
	rec.RegisterState(st) // a fonte do estado só existe depois do nó Raft (ver RegisterState)
	s := adminhttp.New(cfg.AdminAddr, st, rec.Handler())
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
	Store    CellStore
	Pump     *usecase.CommitPump
	Node     *etcdraft.Node
	Applier  *usecase.Applier
	GateSync *usecase.GateSync
	Reads    *usecase.ReadService
	Writer   port.Writer
}

// runRuntime confere as tabelas, abre o gRPC entre células e roda os laços de fundo.
// Erro de qualquer laço (fsync, canal fechado) derruba o processo e o
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
			pump := d.Pump
			fatal("raft", d.Node.Run)
			fatal("commitpump", pump.Run)
			fatal("applier", d.Applier.Run)
			fatal("gatesync", d.GateSync.Run)
			if c := d.Node.Compactor(); c != nil {
				fatal("compactor", c.Run) // fsync do progresso, do marcador ou do WAL: fatal (RT-05)
			}
			d.Log.Info().Str("peer", d.Cfg.PeerAddr).Int("applyWorkers", d.Applier.WorkerCount()).
				Uint64("applyLagMax", d.Cfg.ApplyLagMax).Int("electionTicks", d.Cfg.ElectionTicks).
				Uint64("retomaEm", uint64(d.Pump.From)).Uint64("snapshot", uint64(d.Node.SnapshotIndex())).
				Int64("ramMax", d.Cfg.RaftRAMMax).Int64("retainMax", d.Cfg.RetainMaxBytes).
				Int64("segmento", d.Cfg.WalSegmentBytes).Msg("célula iniciada")
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			peer.Stop()
			cancel()
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select { // os laços param antes de fechar o WAL e o progresso
			case <-done:
				return nil
			case <-stopCtx.Done():
				return stopCtx.Err()
			}
		},
	})
}

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
			func() *domain.CellMode { return &domain.CellMode{} },
			newPeerClient,
			func(c *peerclient.Client) port.Forwarder { return c },
			func(c *peerclient.Client) port.BarrierClient { return c },
			func(c *peerclient.Client) port.GatePublisher { return c },
			func() *domain.ReplicationClock { return &domain.ReplicationClock{} },
			newRecovery,
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
			newReplicationMonitor,
			newRebuild,
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
	// HasItems confere se alguma tabela já tem itens (fail-fast do volume perdido, DC-12).
	HasItems(ctx context.Context, tables []domain.LogicalTable) (bool, error)
}

func newStore(cfg config.Config, tm domain.TableMap) (CellStore, error) {
	return dynamodb.New(context.Background(), cfg.DynamoEndpoint, cfg.AWSRegion, tm, cfg.ApplyWorkers)
}

// Resume é onde a partida retoma a entrega das entradas commitadas (ADR-060, item 5).
type Resume struct {
	From     domain.Index // primeiro índice que o CommitPump entrega
	Progress domain.Index // índice do progresso lido (0 = ausente ou corrompido)
}

// newResume calcula o ponto de retomada a partir do progresso do applier, para o Raft não
// reentregar o log inteiro a cada partida.
func newResume(p port.ApplyProgress) (Resume, error) {
	idx, _, err := p.LoadProgress()
	if err != nil {
		return Resume{}, err
	}
	return Resume{From: usecase.ResumeFrom(idx), Progress: idx}, nil
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
	// ReplClock recebe as amostras da líder (DC-16).
	ReplClock *domain.ReplicationClock
	// Recovery é a política do MsgSnap (RT-18) e o canal de recuperação (AP-17).
	Recovery *usecase.Recovery
	Mode     *domain.CellMode
	Store    CellStore
	Tables   domain.TableMap
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
	var genesis map[uint64]string
	if !etcdraft.HasState(cfg.RaftDir) { // DC-12: volume vazio, só cria o grupo no nascimento
		g, err := preBoot(in, volume)
		if err != nil {
			return nil, err
		}
		genesis = g
	}
	n, err := etcdraft.Open(etcdraft.Config{
		ID: cfg.NodeID, Voters: config.IDs(cfg.Peers),
		Dir: cfg.RaftDir, TickInterval: 100 * time.Millisecond, ElectionTicks: cfg.ElectionTicks,
		Applied: uint64(in.Resume.From) - 1, Metrics: in.Metrics,
		ClusterID: cfg.ClusterID, SegmentBytes: cfg.WalSegmentBytes,
		MarkerPath: filepath.Join(volume, "snapshot"),
		Policy:     in.Recovery,
		Genesis:    genesis,
		Compaction: etcdraft.CompactionConfig{
			Progress: in.Progress, Gate: in.Gate,
			EveryEntries: cfg.SnapshotEveryEntries, EveryBytes: cfg.SnapshotEveryBytes,
			CatchupEntries: cfg.RaftCatchupEntries, RAMMax: cfg.RaftRAMMax,
			RetainMax: cfg.RetainMaxBytes, HardFrac: cfg.RetainDiskHard, HardBytes: cfg.RetainDiskHardBytes,
			Interval: cfg.RaftCompactInterval,
		},
		ReplClock: in.ReplClock,
	}, in.Send, in.Log)
	if err != nil {
		return nil, err
	}
	// DC-04 (O-42, item 1): um progresso atrás do disco retido pularia entradas. A célula
	// começa em reconstruindo, em vez de não subir (substitui o RT-09 antigo e a recusa da
	// O-37, item 7). Fora a partida do zero, em que o snapshot sintético é o índice 1.
	if p, r := in.Resume.Progress, n.FirstIndex(); usecase.StartsRebuilding(p, r) {
		in.Log.Warn().Uint64("progresso", uint64(p)).Uint64("primeiroRetido", uint64(r)).
			Msgf("progresso %d atrás do disco retido %d: reconstrução automática", p, r)
		in.Mode.EnterRebuilding()
	}
	// G6: o confirmado nunca recua depois de um reinício; o do marcador restaura o gate antes
	// de os laços começarem.
	in.Gate.Restore(n.Marker().Confirmed)
	in.Lc.Append(fx.StopHook(n.Close))
	return n, nil
}

// preBootReporter é o /status e o /readyz da pré-partida (DC-12).
type preBootReporter struct{ cfg config.Config }

func (r preBootReporter) Status() usecase.CellStatus {
	return usecase.CellStatus{Celula: r.cfg.CellID, No: r.cfg.NodeID, Papel: "follower",
		Modo: usecase.ModoStatus{Estado: "partida"}}
}
func (preBootReporter) Ready() (bool, string) {
	return false, "partida: decidindo se o grupo está nascendo"
}

// preBootInterval é a cadência da pergunta aos pares na pré-partida.
const preBootInterval = 2 * time.Second

// preBoot decide, com o volume do Raft vazio, se o grupo está nascendo (DC-12): grava o
// token de partida, responde aos pares só com ele (Retained em pré-partida), confere a
// tabela local e pergunta aos pares a cada 2 s até domain.DecideBoot criar ou recusar.
// Devolve o Genesis a gravar no metadado do WAL.
func preBoot(in raftIn, volume string) (map[uint64]string, error) {
	cfg, log := in.Cfg, in.Log
	tok, err := etcdraft.LoadBootToken(filepath.Join(volume, "boot-token"))
	if err != nil {
		return nil, err
	}
	srv := &peergrpc.Server{Self: cfg.NodeID, PreBoot: &peergrpc.PreBoot{ClusterID: cfg.ClusterID, BootToken: tok}}
	lis, err := net.Listen("tcp", cfg.PeerAddr)
	if err != nil {
		return nil, err
	}
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	admin := adminhttp.New(cfg.AdminAddr, preBootReporter{cfg}, nil, nil)
	go func() { _ = admin.ListenAndServe() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = admin.Shutdown(ctx)
	}()
	ctx := context.Background()
	logs := time.Time{}
	var local bool
	for {
		has, err := in.Store.HasItems(ctx, in.Tables.Tables())
		if err == nil {
			local = has
			break
		}
		if now := time.Now(); now.Sub(logs) >= 10*time.Second {
			logs = now
			log.Info().Err(err).Msg("partida: aguardando as tabelas para decidir se o grupo está nascendo")
		}
		time.Sleep(preBootInterval)
	}
	logs = time.Time{}
	for {
		peers := map[uint64]domain.BootPeer{}
		for _, id := range config.IDs(cfg.Peers) {
			if id == cfg.NodeID {
				continue
			}
			info, err := in.Send.Retained(ctx, id)
			if err != nil {
				peers[id] = domain.BootPeer{}
				continue
			}
			peers[id] = domain.BootPeer{Responded: true, ClusterID: info.ClusterID, EmptyVolume: info.EmptyVolume,
				BootToken: info.BootToken, Genesis: info.Genesis}
		}
		d := domain.DecideBoot(cfg.NodeID, cfg.ClusterID, tok, local, peers)
		switch d.Kind {
		case domain.BootCreate:
			log.Info().Interface("genesis", d.Genesis).Msg("partida: o grupo está nascendo; criando o WAL")
			return d.Genesis, nil
		case domain.BootRefuse:
			return nil, fmt.Errorf("%w: %s; a célula não sobe. Devolver a célula ao grupo é a feature troca-de-pvc-em-cell-ativa (parklot/adr-troca-de-pvc-em-cell-ativa.md); na POC, rode task clean",
				usecase.ErrLostRaftVolume, d.Reason)
		}
		if now := time.Now(); now.Sub(logs) >= 10*time.Second {
			logs = now
			log.Info().Msgf("aguardando os pares %v para decidir se o grupo está nascendo", d.Waiting)
		}
		time.Sleep(preBootInterval)
	}
}

// newRecovery é a política do MsgSnap e o canal de recuperação (AP-17, DC-02).
func newRecovery(cfg config.Config, pc *peerclient.Client, mode *domain.CellMode, g *domain.Gate, a *usecase.Applier,
	m port.Metrics, log zerolog.Logger) *usecase.Recovery {
	return &usecase.Recovery{Self: cfg.NodeID, Peers: config.IDs(cfg.Peers), Client: pc, Mode: mode, Gate: g, Applier: a,
		Retry: cfg.RebuildRetry, Metrics: m, Log: log}
}

// newRebuild é a reconstrução por cópia da tabela (AP-18, DC-04, DC-11).
func newRebuild(cfg config.Config, pc *peerclient.Client, st CellStore, n *etcdraft.Node, p port.ApplyProgress,
	mode *domain.CellMode, a *usecase.Applier, inbox chan []domain.Entry, tm domain.TableMap, m port.Metrics,
	log zerolog.Logger) *usecase.Rebuild {
	return &usecase.Rebuild{Self: cfg.NodeID, Peers: config.IDs(cfg.Peers), Client: pc, Catalog: st,
		Identity: identityOf(n), Store: st, Progress: p, Mode: mode,
		Idle:     func() bool { return a.Idle() && len(inbox) == 0 },
		IsLeader: func() bool { l, ok := n.Leader(); return ok && l.Local },
		Tables:   tm.Tables(), Retry: cfg.RebuildRetry,
		Backoff: usecase.Backoff{Min: 100 * time.Millisecond, Max: 5 * time.Second}, Metrics: m, Log: log}
}

// identityOf é o cluster_id e o epoch do marcador local (DC-11).
func identityOf(n *etcdraft.Node) func() (string, uint64) {
	return func() (string, uint64) {
		mk := n.Marker()
		return mk.ClusterID, mk.Epoch
	}
}

func newApplier(cfg config.Config, g *domain.Gate, st port.StateStore, p port.ApplyProgress, in chan []domain.Entry, log zerolog.Logger, m port.Metrics) *usecase.Applier {
	return &usecase.Applier{Gate: g, Store: st, Progress: p, In: in, Workers: cfg.ApplyWorkers,
		Backoff: usecase.Backoff{Min: 100 * time.Millisecond, Max: 5 * time.Second}, Log: log, Metrics: m}
}

func newReadService(cfg config.Config, c port.ConsensusEngine, g *domain.Gate, a port.AppliedWaiter, b port.BarrierClient,
	st port.StateStore, sc port.Scanner, m port.Metrics, mode *domain.CellMode) *usecase.ReadService {
	return &usecase.ReadService{Consensus: c, Gate: g, Applied: a, Barrier: b, Store: st, Scanner: sc, WaitLeader: cfg.WaitLeader,
		Metrics: m, Mode: mode}
}

// newGateSync: o piso de retenção vai junto com o gate (RT-15). Na líder, o Compactor o
// calcula; na follower, a retenção local recebe o da líder.
// O aplicado local volta à líder na resposta do GateUpdate (DC-15).
func newGateSync(cfg config.Config, c port.ConsensusEngine, g *domain.Gate, p port.GatePublisher, log zerolog.Logger,
	n *etcdraft.Node, a *usecase.Applier) *usecase.GateSync {
	return &usecase.GateSync{Consensus: c, Gate: g, Publisher: p, Interval: cfg.GatePublishInterval, Log: log,
		Floor: n.Floor, FloorSink: n.Pins(), Applied: a.Applied}
}

// newReplicationMonitor mede a replicação até o banco na líder (D-04). O peerclient
// entrega a ele o aplicado das followers: o campo é definido aqui, antes de qualquer
// Publish (o grafo não fecha ciclo: o nó Raft depende do peerclient).
func newReplicationMonitor(cfg config.Config, c port.ConsensusEngine, clk *domain.ReplicationClock, a *usecase.Applier,
	pc *peerclient.Client, m port.Metrics, log zerolog.Logger) *usecase.ReplicationMonitor {
	mon := &usecase.ReplicationMonitor{Consensus: c, Clock: clk, LocalApplied: a.Applied, NodeID: cfg.NodeID,
		Voters: config.IDs(cfg.Peers), SyncMax: cfg.ReplSyncMax, Interval: cfg.GatePublishInterval, Metrics: m, Log: log}
	pc.AppliedSink = mon
	return mon
}

// newCommitPump: em recuperando, o pump lê do Recovery (DC-03).
func newCommitPump(n *etcdraft.Node, g *domain.Gate, to chan []domain.Entry, r Resume, mode *domain.CellMode,
	rec *usecase.Recovery, a *usecase.Applier, log zerolog.Logger) *usecase.CommitPump {
	return &usecase.CommitPump{Consensus: n, Gate: g, ToApplier: to, From: r.From, Mode: mode, Remote: rec, Applied: a, Log: log}
}

type statusIn struct {
	fx.In
	Cfg      config.Config
	Raft     port.RaftStatus
	Gate     *domain.Gate
	Applier  *usecase.Applier
	Catalog  port.Catalog
	Pump     *usecase.CommitPump
	Inbox    chan []domain.Entry
	Monitor  *usecase.ReplicationMonitor
	Mode     *domain.CellMode
	Recovery *usecase.Recovery
	Rebuild  *usecase.Rebuild
}

func newStatus(in statusIn) *usecase.StatusReporter {
	cfg := in.Cfg
	return &usecase.StatusReporter{CellID: cfg.CellID, NodeID: cfg.NodeID, Raft: in.Raft, Gate: in.Gate, Applier: in.Applier,
		Catalog: in.Catalog, Pump: in.Pump, ApplyInbox: func() int { return len(in.Inbox) },
		ApplyLagMax: cfg.ApplyLagMax, ElectionTicks: cfg.ElectionTicks, Replication: in.Monitor,
		Mode: in.Mode, Recovery: in.Recovery, Rebuild: in.Rebuild,
		Compaction: usecase.CompactacaoStatus{SnapshotEveryEntries: cfg.SnapshotEveryEntries,
			RaftCatchupEntries: cfg.RaftCatchupEntries, RaftRAMMax: cfg.RaftRAMMax, RetainMaxBytes: cfg.RetainMaxBytes,
			WalSegmentBytes: cfg.WalSegmentBytes, RetainDiskHardBytes: cfg.RetainDiskHardBytes}}
}

// runAdmin abre a administração primeiro: o /livez responde enquanto as tabelas são
// conferidas.
func runAdmin(lc fx.Lifecycle, cfg config.Config, st *usecase.StatusReporter, rec *prommetrics.Recorder, rb *usecase.Rebuild,
	log zerolog.Logger) {
	rec.RegisterState(st) // a fonte do estado só existe depois do nó Raft (ver RegisterState)
	s := adminhttp.New(cfg.AdminAddr, st, rec.Handler(), rb)
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
	// ReplMonitor mede a replicação até o banco na líder (D-04).
	ReplMonitor *usecase.ReplicationMonitor
	Reads       *usecase.ReadService
	Writer      port.Writer
	// Canal de recuperação servido aos pares (RT-19) e a reconstrução local (AP-18).
	Mode    *domain.CellMode
	Gate    *domain.Gate
	Tables  domain.TableMap
	Rebuild *usecase.Rebuild
}

// runRuntime confere as tabelas, abre o gRPC entre células e roda os laços de fundo.
// Erro de qualquer laço (fsync, canal fechado) derruba o processo e o
// Docker reinicia (RT-05). O Applier estacionado não é erro: o processo fica de pé.
func runRuntime(d runtimeDeps) {
	ctx, cancel := context.WithCancel(context.Background())
	peer := &peergrpc.Server{Self: d.Cfg.NodeID, Raft: d.Node, Writer: d.Writer, Barrier: d.Reads, Gate: d.GateSync,
		Log: d.Node.LogReader(), Pins: d.Node.Pins(), Scanner: d.Store, Catalog: d.Store, Mode: d.Mode, SourceGate: d.Gate,
		Applied: d.Applier, Status: d.Node, Tables: d.Tables.Tables(), PinTTL: d.Cfg.RebuildPinTTL,
		Identity: identityOf(d.Node), Genesis: d.Node.Genesis}
	var wg sync.WaitGroup
	fatal := func(name string, run func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := run(ctx)
			switch {
			case err == nil, errors.Is(err, context.Canceled):
			case errors.Is(err, usecase.ErrRestartAfterRebuild):
				// DC-04: a única saída intencional; o compose reinicia (restart: on-failure).
				d.Log.Info().Err(err).Str("laco", name).Msg("reiniciando a célula depois da reconstrução")
				os.Exit(1)
			default:
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
			fatal("replmonitor", d.ReplMonitor.Run)
			fatal("rebuild", d.Rebuild.Run)
			if c := d.Node.Compactor(); c != nil {
				fatal("compactor", c.Run) // fsync do progresso, do marcador ou do WAL: fatal (RT-05)
			}
			d.Log.Info().Str("peer", d.Cfg.PeerAddr).Int("applyWorkers", d.Applier.WorkerCount()).
				Uint64("applyLagMax", d.Cfg.ApplyLagMax).Int("electionTicks", d.Cfg.ElectionTicks).
				Uint64("retomaEm", uint64(d.Pump.From)).Uint64("snapshot", uint64(d.Node.SnapshotIndex())).
				Int64("ramMax", d.Cfg.RaftRAMMax).Int64("retainMax", d.Cfg.RetainMaxBytes).
				Int64("segmento", d.Cfg.WalSegmentBytes).Dur("replSyncMax", d.Cfg.ReplSyncMax).
				Str("estado", d.Mode.State().String()).Msg("célula iniciada")
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

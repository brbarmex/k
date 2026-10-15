// Package prommetrics é o adaptador de métricas Prometheus (O-15). É o ÚNICO pacote que
// importa o client_golang. Eventos chegam pela porta port.Metrics; o estado da célula é
// lido na hora da coleta (StateCollector), a partir do mesmo retrato do /status.
package prommetrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// StatusSource é de onde o coletor lê o estado (usecase.StatusReporter).
type StatusSource interface {
	Status() usecase.CellStatus
	Ready() (bool, string)
}

// Valores pré-inicializados dos rótulos, para as séries existirem desde a partida.
var (
	MsgTypes     = []string{"app", "app_resp", "vote", "vote_resp", "prevote", "prevote_resp", "heartbeat", "heartbeat_resp", "snap", "outro"}
	WriteResults = []string{"ok", "no_leader", "pending", "throttling", "lease_expired", "recusada", "erro"}
	ReadOps      = []string{"get", "scan"}
	ReadResults  = []string{"ok", "no_leader", "gate_not_ready", "read_timeout", "applier_stopped", "recusada", "erro"}
	// ThrottleCauses são as causas da contrapressão (O-27, ADR-060).
	ThrottleCauses = []string{"buffer_cheio", "nao_lider", "transferencia", "atraso_apply"}
	// HTTPOps são as operações pré-inicializadas na porta de API (O-28).
	HTTPOps = []string{"PutItem", "GetItem", "Scan"}
)

// Recorder implementa port.Metrics. Os métodos só fazem incrementos atômicos e medições:
// nunca bloqueiam o laço do Ready (salvaguarda de métricas).
type Recorder struct {
	registry *prometheus.Registry
	wrapped  prometheus.Registerer

	leaderChanges prometheus.Counter
	msgSent       *prometheus.CounterVec
	msgReceived   *prometheus.CounterVec
	lastHeartbeat prometheus.Gauge
	walFsync      prometheus.Histogram
	sendDropped   *prometheus.CounterVec
	forwards      prometheus.Counter
	writes        *prometheus.CounterVec
	writeDuration prometheus.Histogram
	reads         *prometheus.CounterVec
	readBarrier   prometheus.Histogram
	applyItem     prometheus.Histogram
	replogSync    prometheus.Histogram
	raftLoop      prometheus.Histogram
	raftTickGap   prometheus.Histogram
	throttled     *prometheus.CounterVec
	httpRequests  *prometheus.CounterVec
	httpDuration  *prometheus.HistogramVec
	httpInFlight  prometheus.Gauge
}

var _ port.Metrics = (*Recorder)(nil)

// NewRecorder cria o registro próprio, com o rótulo constante `celula` em todas as séries.
func NewRecorder(celula string, source StatusSource) *Recorder {
	reg := prometheus.NewRegistry()
	wrapped := prometheus.WrapRegistererWith(prometheus.Labels{"celula": celula}, reg)
	f := func(c prometheus.Collector) prometheus.Collector { wrapped.MustRegister(c); return c }

	r := &Recorder{registry: reg, wrapped: wrapped}
	r.leaderChanges = f(prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dr_leader_changes_total", Help: "Quantas vezes a célula viu a líder mudar (inclusive para sem líder).",
	})).(prometheus.Counter)
	r.msgSent = f(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dr_raft_messages_sent_total", Help: "Mensagens do Raft enviadas pela célula, por tipo.",
	}, []string{"tipo"})).(*prometheus.CounterVec)
	r.msgReceived = f(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dr_raft_messages_received_total", Help: "Mensagens do Raft recebidas pela célula, por tipo.",
	}, []string{"tipo"})).(*prometheus.CounterVec)
	r.lastHeartbeat = f(prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dr_raft_last_heartbeat_timestamp_seconds", Help: "Instante Unix do último heartbeat (ou resposta de heartbeat) visto pela célula.",
	})).(prometheus.Gauge)
	r.walFsync = f(prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "dr_wal_fsync_duration_seconds", Help: "Duração do wal.Save (gravação com fdatasync) de cada Ready.",
		Buckets: prometheus.ExponentialBucketsRange(0.0005, 1, 12),
	})).(prometheus.Histogram)
	r.sendDropped = f(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dr_peer_send_dropped_total", Help: "Mensagens do Raft descartadas por fila cheia, por par de destino.",
	}, []string{"par"})).(*prometheus.CounterVec)
	r.forwards = f(prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dr_forwards_total", Help: "Escritas encaminhadas por esta célula (follower) à líder.",
	})).(prometheus.Counter)
	r.writes = f(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dr_writes_total", Help: "Escritas (PutItem) recebidas pela célula, por resultado; ok é o ACK.",
	}, []string{"resultado"})).(*prometheus.CounterVec)
	r.writeDuration = f(prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "dr_write_duration_seconds", Help: "Latência das escritas confirmadas (ACK), da chegada à resposta.",
		Buckets: prometheus.ExponentialBucketsRange(0.001, 10, 14),
	})).(prometheus.Histogram)
	r.reads = f(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dr_reads_total", Help: "Leituras fortes recebidas pela célula, por operação e resultado.",
	}, []string{"op", "resultado"})).(*prometheus.CounterVec)
	r.readBarrier = f(prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "dr_read_barrier_duration_seconds", Help: "Duração da barreira de leitura forte mais a espera do apply local.",
		Buckets: prometheus.ExponentialBucketsRange(0.0005, 5, 14),
	})).(prometheus.Histogram)
	r.applyItem = f(prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "dr_apply_item_duration_seconds", Help: "Duração da gravação de um item no banco da célula pelo applier, com as repetições (ADR-058).",
		Buckets: prometheus.ExponentialBucketsRange(0.0005, 10, 16),
	})).(prometheus.Histogram)
	r.replogSync = f(prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "dr_replog_sync_duration_seconds", Help: "Duração do fsync do checkpoint do log de replicação, fora da trava do Append (ADR-059).",
		Buckets: prometheus.ExponentialBucketsRange(0.0005, 5, 14),
	})).(prometheus.Histogram)

	r.raftLoop = f(prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "dr_raft_loop_duration_seconds", Help: "Duração de cada Ready no laço do Raft (fsync, envio, publicação do commit). Acima do tick, o heartbeat atrasa (ADR-060).",
		Buckets: prometheus.ExponentialBucketsRange(0.0001, 5, 16),
	})).(prometheus.Histogram)
	r.raftTickGap = f(prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "dr_raft_tick_gap_seconds", Help: "Intervalo real entre dois ticks do Raft (o esperado é 0,1 s). Perto do timeout de eleição, a célula some para as outras.",
		Buckets: []float64{0.11, 0.15, 0.2, 0.3, 0.5, 1, 2, 3, 5, 10},
	})).(prometheus.Histogram)
	r.throttled = f(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dr_throttled_total", Help: "Escritas recusadas com ThrottlingException, por causa, na célula onde a causa nasceu (ADR-060).",
	}, []string{"causa"})).(*prometheus.CounterVec)
	r.httpRequests = f(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dr_http_requests_total", Help: "Requisições terminadas na porta de API, por operação do DynamoDB e código HTTP. rate() dá o TPS de entrada da célula.",
	}, []string{"op", "codigo"})).(*prometheus.CounterVec)
	r.httpDuration = f(prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "dr_http_request_duration_seconds", Help: "Latência de toda requisição na porta de API, da chegada à resposta, com qualquer resultado.",
		Buckets: prometheus.ExponentialBucketsRange(0.0005, 10, 16),
	}, []string{"op"})).(*prometheus.HistogramVec)
	r.httpInFlight = f(prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dr_http_inflight", Help: "Requisições em andamento na porta de API.",
	})).(prometheus.Gauge)

	for _, c := range ThrottleCauses {
		r.throttled.WithLabelValues(c)
	}
	for _, op := range HTTPOps {
		r.httpDuration.WithLabelValues(op)
		for _, code := range []string{"200", "400", "503"} {
			r.httpRequests.WithLabelValues(op, code)
		}
	}
	for _, t := range MsgTypes {
		r.msgSent.WithLabelValues(t)
		r.msgReceived.WithLabelValues(t)
	}
	for _, res := range WriteResults {
		r.writes.WithLabelValues(res)
	}
	for _, op := range ReadOps {
		for _, res := range ReadResults {
			r.reads.WithLabelValues(op, res)
		}
	}

	if source != nil {
		r.RegisterState(source)
	}
	wrapped.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return r
}

func (r *Recorder) LeaderChanged(uint64)        { r.leaderChanges.Inc() }
func (r *Recorder) RaftMessageSent(tipo string) { r.msgSent.WithLabelValues(tipo).Inc() }
func (r *Recorder) RaftMessageReceived(tipo string) {
	r.msgReceived.WithLabelValues(tipo).Inc()
}
func (r *Recorder) HeartbeatSeen()           { r.lastHeartbeat.SetToCurrentTime() }
func (r *Recorder) WALFsync(d time.Duration) { r.walFsync.Observe(d.Seconds()) }
func (r *Recorder) PeerSendDropped(par uint64) {
	r.sendDropped.WithLabelValues(strconv.FormatUint(par, 10)).Inc()
}
func (r *Recorder) Forwarded() { r.forwards.Inc() }
func (r *Recorder) WriteDone(resultado string, d time.Duration) {
	r.writes.WithLabelValues(resultado).Inc()
	if resultado == "ok" {
		r.writeDuration.Observe(d.Seconds())
	}
}
func (r *Recorder) ReadDone(op, resultado string) { r.reads.WithLabelValues(op, resultado).Inc() }
func (r *Recorder) ReadBarrier(d time.Duration)   { r.readBarrier.Observe(d.Seconds()) }
func (r *Recorder) ApplyItem(d time.Duration)     { r.applyItem.Observe(d.Seconds()) }
func (r *Recorder) ReplogSync(d time.Duration)    { r.replogSync.Observe(d.Seconds()) }
func (r *Recorder) RaftLoop(d time.Duration)      { r.raftLoop.Observe(d.Seconds()) }
func (r *Recorder) RaftTickGap(d time.Duration)   { r.raftTickGap.Observe(d.Seconds()) }
func (r *Recorder) Throttled(causa string)        { r.throttled.WithLabelValues(causa).Inc() }
func (r *Recorder) HTTPRequest(op string, status int, d time.Duration) {
	r.httpRequests.WithLabelValues(op, strconv.Itoa(status)).Inc()
	r.httpDuration.WithLabelValues(op).Observe(d.Seconds())
}
func (r *Recorder) HTTPInFlight(delta int) { r.httpInFlight.Add(float64(delta)) }

// RegisterState registra o coletor de estado. Existe porque a fonte (StatusReporter)
// depende do nó Raft, que por sua vez recebe este Recorder: o bootstrap liga a fonte
// depois de montar o grafo. Chamar uma vez só.
func (r *Recorder) RegisterState(source StatusSource) {
	r.wrapped.MustRegister(NewStateCollector(source))
}

// Handler serve o /metrics do registro próprio.
func (r *Recorder) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

// Registry expõe o registro (testes).
func (r *Recorder) Registry() *prometheus.Registry { return r.registry }

package port

import "time"

// Metrics registra EVENTOS observáveis (contadores e histogramas). O ESTADO da célula
// (líder, termo, commit, gate, aplicado, quórum) não passa por aqui: é lido na coleta a
// partir do mesmo retrato do /status.
//
// Todos os métodos são seguros para uso concorrente, não devolvem nada e NUNCA bloqueiam:
// são chamados de dentro do laço do Ready do Raft.
type Metrics interface {
	// LeaderChanged: a célula viu uma líder diferente da anterior (0 = sem líder).
	LeaderChanged(lead uint64)
	// RaftMessageSent e RaftMessageReceived: tipo em app, app_resp, vote, vote_resp,
	// prevote, prevote_resp, heartbeat, heartbeat_resp, snap, outro.
	RaftMessageSent(tipo string)
	RaftMessageReceived(tipo string)
	// HeartbeatSeen: chegou MsgHeartbeat ou MsgHeartbeatResp.
	HeartbeatSeen()
	// WALFsync: duração do wal.Save de um Ready com entradas ou HardState.
	WALFsync(d time.Duration)
	// PeerSendDropped: mensagem do Raft descartada por fila cheia.
	PeerSendDropped(par uint64)
	// Forwarded: escrita encaminhada pela follower à líder.
	Forwarded()
	// WriteDone: resultado em ok, no_leader, pending, throttling, lease_expired, recusada, erro.
	WriteDone(resultado string, d time.Duration)
	// ReadDone: op em get, scan; resultado em ok, no_leader, gate_not_ready, read_timeout,
	// recusada, erro.
	ReadDone(op string, resultado string)
	// ReadBarrier: duração da barreira de leitura mais a espera do apply.
	ReadBarrier(d time.Duration)
	// ApplyItem: duração da gravação de UM item no banco da célula pelo applier, com as
	// repetições de erro transitório (ADR-058).
	ApplyItem(d time.Duration)
	// ReplogSync: duração do fsync do checkpoint do log de replicação (ADR-059).
	ReplogSync(d time.Duration)
}

// NopMetrics não faz nada: usada nos testes e quando nenhuma métrica é injetada.
type NopMetrics struct{}

var _ Metrics = NopMetrics{}

func (NopMetrics) LeaderChanged(uint64)            {}
func (NopMetrics) RaftMessageSent(string)          {}
func (NopMetrics) RaftMessageReceived(string)      {}
func (NopMetrics) HeartbeatSeen()                  {}
func (NopMetrics) WALFsync(time.Duration)          {}
func (NopMetrics) PeerSendDropped(uint64)          {}
func (NopMetrics) Forwarded()                      {}
func (NopMetrics) WriteDone(string, time.Duration) {}
func (NopMetrics) ReadDone(string, string)         {}
func (NopMetrics) ReadBarrier(time.Duration)       {}
func (NopMetrics) ApplyItem(time.Duration)         {}
func (NopMetrics) ReplogSync(time.Duration)        {}

// MetricsOrNop devolve m, ou NopMetrics quando m é nulo.
func MetricsOrNop(m Metrics) Metrics {
	if m == nil {
		return NopMetrics{}
	}
	return m
}

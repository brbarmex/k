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
	// WriteDone: resultado em ok, no_leader, pending, throttling, recusada, erro.
	WriteDone(resultado string, d time.Duration)
	// ReadDone: op em get, scan; resultado em ok, no_leader, read_timeout,
	// recusada, erro.
	ReadDone(op string, resultado string)
	// ReadBarrier: duração da barreira de leitura mais a espera do apply.
	ReadBarrier(d time.Duration)
	// ApplyItem: duração da gravação de UM item no banco da célula pelo applier, com as
	// repetições de erro transitório (ADR-058).
	ApplyItem(d time.Duration)

	// RaftLoop: duração de um Ready inteiro no laço do Raft (fsync, envio, publicação do
	// commit). Laço lento = heartbeat atrasado (ADR-060, O-27).
	RaftLoop(d time.Duration)
	// RaftTickGap: intervalo real entre dois ticks do Raft. Acima do TickInterval, o laço
	// esteve preso e a célula ficou sem heartbeat nem contagem de eleição.
	RaftTickGap(d time.Duration)
	// Throttled: escrita recusada por contrapressão, por causa: buffer_cheio, nao_lider,
	// atraso_apply. Registrada na célula onde a causa nasceu.
	Throttled(causa string)
	// HTTPRequest: requisição terminada na porta de API (O-28). op é a operação do
	// DynamoDB (PutItem, GetItem, Scan, ...; "outra" se desconhecida) e status o código
	// HTTP devolvido. Dá o TPS de entrada por célula.
	HTTPRequest(op string, status int, d time.Duration)
	// HTTPInFlight: requisições em andamento na porta de API (+1 na chegada, -1 na saída).
	HTTPInFlight(delta int)
}

// NopMetrics não faz nada: usada nos testes e quando nenhuma métrica é injetada.
type NopMetrics struct{}

var _ Metrics = NopMetrics{}

func (NopMetrics) LeaderChanged(uint64)                   {}
func (NopMetrics) RaftMessageSent(string)                 {}
func (NopMetrics) RaftMessageReceived(string)             {}
func (NopMetrics) HeartbeatSeen()                         {}
func (NopMetrics) WALFsync(time.Duration)                 {}
func (NopMetrics) PeerSendDropped(uint64)                 {}
func (NopMetrics) Forwarded()                             {}
func (NopMetrics) WriteDone(string, time.Duration)        {}
func (NopMetrics) ReadDone(string, string)                {}
func (NopMetrics) ReadBarrier(time.Duration)              {}
func (NopMetrics) ApplyItem(time.Duration)                {}
func (NopMetrics) RaftLoop(time.Duration)                 {}
func (NopMetrics) RaftTickGap(time.Duration)              {}
func (NopMetrics) Throttled(string)                       {}
func (NopMetrics) HTTPRequest(string, int, time.Duration) {}
func (NopMetrics) HTTPInFlight(int)                       {}

// MetricsOrNop devolve m, ou NopMetrics quando m é nulo.
func MetricsOrNop(m Metrics) Metrics {
	if m == nil {
		return NopMetrics{}
	}
	return m
}

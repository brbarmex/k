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

	// SnapshotTaken: duração de um snapshot-marcador, do fsync do progresso ao
	// CreateSnapshot (RT-11).
	SnapshotTaken(d time.Duration)
	// SnapshotDeferred: MsgSnap recebido e descartado porque o aplicado local está atrás
	// do índice do marcador (RT-18, SG-19).
	SnapshotDeferred()
	// StartupDuration: duração da abertura do WAL e da releitura da cauda na partida (RT-09).
	StartupDuration(d time.Duration)

	// ReplicationStage: tempo de replicação no relógio da líder (D-04), por etapa em
	// commit, banco_lider, banco_quorum, banco_todas. Cada observação é uma amostra do
	// ReplicationClock (DC-16).
	ReplicationStage(etapa string, d time.Duration)
	// RecoveryFinished: sessão de recuperação encerrada, medida pela líder (DC-17).
	RecoveryFinished(par uint64, d time.Duration)
	// RAMCeilingHit: um ciclo do Compactor encontrou a RAM do Raft acima de RAFT_RAM_MAX e
	// compactou até o snapshot, ignorando o Match (SG-23).
	RAMCeilingHit()
	// CatchUpEntries: entradas recebidas pelo canal de recuperação (AP-17).
	CatchUpEntries(n int)
	// RebuildItems: itens gravados pela cópia da reconstrução (AP-18).
	RebuildItems(n int)
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
func (NopMetrics) SnapshotTaken(time.Duration)            {}
func (NopMetrics) SnapshotDeferred()                      {}
func (NopMetrics) StartupDuration(time.Duration)          {}
func (NopMetrics) ReplicationStage(string, time.Duration) {}
func (NopMetrics) RecoveryFinished(uint64, time.Duration) {}
func (NopMetrics) RAMCeilingHit()                         {}
func (NopMetrics) CatchUpEntries(int)                     {}
func (NopMetrics) RebuildItems(int)                       {}

// MetricsOrNop devolve m, ou NopMetrics quando m é nulo.
func MetricsOrNop(m Metrics) Metrics {
	if m == nil {
		return NopMetrics{}
	}
	return m
}

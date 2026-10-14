package prommetrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// StateCollector exporta o estado da célula lido na hora da coleta, a partir do mesmo
// retrato do /status. Roda na goroutine do scrape: nunca toca o laço do Ready.
type StateCollector struct {
	source StatusSource

	isLeader, leaderID, term             *prometheus.Desc
	commit, gate, gateReady, applied     *prometheus.Desc
	replogLast, replogDurable            *prometheus.Desc
	quorumActive, quorumVoters, quorumOk *prometheus.Desc
	peerMatch, peerLag                   *prometheus.Desc
	applierStopped, ready, azureOff      *prometheus.Desc
	applyWorkers, applyInFlight          *prometheus.Desc
	applyLag, applyLagMax, applyInbox    *prometheus.Desc
	commitUndelivered, electionTicks     *prometheus.Desc
}

// NewStateCollector cria o coletor de estado.
func NewStateCollector(source StatusSource) *StateCollector {
	d := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(name, help, labels, nil)
	}
	return &StateCollector{
		source:            source,
		isLeader:          d("dr_is_leader", "1 se esta célula é a líder, 0 se é follower."),
		leaderID:          d("dr_leader_id", "Id da líder conhecida por esta célula (0 = sem líder)."),
		term:              d("dr_raft_term", "Termo atual do Raft visto por esta célula."),
		commit:            d("dr_commit_index", "Último índice commitado pelo Raft nesta célula."),
		gate:              d("dr_gate_index", "Índice até onde o gate libera ACK e apply nesta célula."),
		gateReady:         d("dr_gate_ready", "1 se o gate está pronto para leitura forte."),
		applied:           d("dr_applied_index", "Último índice aplicado no banco desta célula."),
		replogLast:        d("dr_replog_last_index", "Último índice gravado no log de replicação."),
		replogDurable:     d("dr_replog_durable_index", "Último índice durável (com fsync) no log de replicação."),
		quorumActive:      d("dr_quorum_active", "Votantes ativos recentemente vistos pela líder (contando ela). Só na líder."),
		quorumVoters:      d("dr_quorum_voters", "Número de votantes do cluster. Só na líder."),
		quorumOk:          d("dr_quorum_ok", "1 se a líder vê a maioria dos votantes ativa. Só na líder."),
		peerMatch:         d("dr_peer_match_index", "Último índice confirmado por cada par. Só na líder.", "par"),
		peerLag:           d("dr_peer_lag", "Entradas que cada par está atrás do commit da líder. Só na líder.", "par"),
		applierStopped:    d("dr_applier_stopped", "1 se o applier desta célula está parado numa entrada venenosa."),
		ready:             d("dr_ready", "1 se a célula está pronta (/readyz 200)."),
		azureOff:          d("dr_azure_off", "1 no modo sem Azure da POC (G-08)."),
		applyWorkers:      d("dr_apply_workers", "Workers do applier desta célula: itens gravados em paralelo, por chave (ADR-058)."),
		applyInFlight:     d("dr_apply_inflight", "Itens despachados ao banco e ainda não concluídos pelo applier desta célula."),
		applyLag:          d("dr_apply_lag", "Entradas que o apply desta célula está atrás do gate (gate - aplicado)."),
		applyLagMax:       d("dr_apply_lag_max", "APPLY_LAG_MAX: acima deste atraso a líder recusa escritas com ThrottlingException (ADR-060)."),
		applyInbox:        d("dr_apply_inbox_batches", "Lotes entregues pelo CommitPump e ainda não despachados pelo applier."),
		commitUndelivered: d("dr_commit_undelivered", "Entradas commitadas que o CommitPump ainda não entregou ao applier (ficam na memória do Raft, ADR-060)."),
		electionTicks:     d("dr_election_ticks", "ELECTION_TICKS: timeout de eleição em ticks de 100 ms."),
	}
}

func (c *StateCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.isLeader, c.leaderID, c.term, c.commit, c.gate, c.gateReady, c.applied,
		c.replogLast, c.replogDurable, c.quorumActive, c.quorumVoters, c.quorumOk, c.peerMatch, c.peerLag,
		c.applierStopped, c.ready, c.azureOff, c.applyWorkers, c.applyInFlight,
		c.applyLag, c.applyLagMax, c.applyInbox, c.commitUndelivered, c.electionTicks} {
		ch <- d
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (c *StateCollector) Collect(ch chan<- prometheus.Metric) {
	st := c.source.Status()
	ok, _ := c.source.Ready()
	g := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	g(c.isLeader, b2f(st.Papel == "lider"))
	g(c.leaderID, float64(st.Lider))
	g(c.term, float64(st.Termo))
	g(c.commit, float64(st.Commit))
	g(c.gate, float64(st.Gate))
	g(c.gateReady, b2f(st.GatePronto))
	g(c.applied, float64(st.Aplicado))
	g(c.replogLast, float64(st.Replog.Ultimo))
	g(c.replogDurable, float64(st.Replog.Duravel))
	if q := st.Quorum; q != nil {
		g(c.quorumActive, float64(q.Ativos))
		g(c.quorumVoters, float64(q.Votantes))
		g(c.quorumOk, b2f(q.Ok))
	}
	for par, m := range st.Match {
		if par == strconv.FormatUint(st.No, 10) {
			continue // a própria líder não é par
		}
		g(c.peerMatch, float64(m), par)
		lag := float64(0)
		if uint64(st.Commit) > m {
			lag = float64(uint64(st.Commit) - m)
		}
		g(c.peerLag, lag, par)
	}
	g(c.applierStopped, b2f(st.ErroApply != ""))
	g(c.ready, b2f(ok))
	g(c.azureOff, b2f(st.Azure == "off"))
	g(c.applyWorkers, float64(st.Apply.Workers))
	g(c.applyInFlight, float64(st.Apply.EmVoo))
	g(c.applyLag, float64(st.Apply.Atraso))
	g(c.applyLagMax, float64(st.Apply.AtrasoMax))
	g(c.applyInbox, float64(st.Apply.Fila))
	g(c.commitUndelivered, float64(st.CommitNaoEntregue))
	g(c.electionTicks, float64(st.ElectionTicks))
}

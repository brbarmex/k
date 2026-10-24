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
	commit, gate, applied                *prometheus.Desc
	quorumActive, quorumVoters, quorumOk *prometheus.Desc
	peerMatch, peerLag                   *prometheus.Desc
	applierStopped, ready                *prometheus.Desc
	applyWorkers, applyInFlight          *prometheus.Desc
	applyLag, applyLagMax, applyInbox    *prometheus.Desc
	commitUndelivered, electionTicks     *prometheus.Desc

	// Log do Raft na RAM (M-04, DC-14).
	memBytes, memEntries, memMax, memPeer *prometheus.Desc
	snapshotIndex, behindTail             *prometheus.Desc
	// Replicação até o banco, vista pela líder (D-04, DC-13, DC-17).
	replApplied, replKnown, replAge, replLag     *prometheus.Desc
	replDelay, replSaturated, replSynced         *prometheus.Desc
	recoveryOn, recoveryElapsed, behindLeaderRAM *prometheus.Desc
	replSyncMax, replHorizon                     *prometheus.Desc
	// Log do Raft no disco e estado da célula (O-42, RT-15, AP-16).
	walRetained, walFiles, retentionFloor, walPins *prometheus.Desc
	cellState                                      *prometheus.Desc
}

// CellStates são os três valores do rótulo estado de dr_cell_state, sempre exportados.
var CellStates = []string{"normal", "recuperando", "reconstruindo"}

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
		applied:           d("dr_applied_index", "Último índice aplicado no banco desta célula."),
		quorumActive:      d("dr_quorum_active", "Votantes ativos recentemente vistos pela líder (contando ela). Só na líder."),
		quorumVoters:      d("dr_quorum_voters", "Número de votantes do cluster. Só na líder."),
		quorumOk:          d("dr_quorum_ok", "1 se a líder vê a maioria dos votantes ativa. Só na líder."),
		peerMatch:         d("dr_peer_match_index", "Último índice confirmado por cada par. Só na líder.", "par"),
		peerLag:           d("dr_peer_lag", "Entradas que cada par está atrás do commit da líder. Só na líder.", "par"),
		applierStopped:    d("dr_applier_stopped", "1 se o applier desta célula está parado numa entrada venenosa."),
		ready:             d("dr_ready", "1 se a célula está pronta (/readyz 200)."),
		applyWorkers:      d("dr_apply_workers", "Workers do applier desta célula: itens gravados em paralelo, por chave (ADR-058)."),
		applyInFlight:     d("dr_apply_inflight", "Itens despachados ao banco e ainda não concluídos pelo applier desta célula."),
		applyLag:          d("dr_apply_lag", "Entradas que o apply desta célula está atrás do gate (gate - aplicado)."),
		applyLagMax:       d("dr_apply_lag_max", "APPLY_LAG_MAX: acima deste atraso a líder recusa escritas com ThrottlingException (ADR-060)."),
		applyInbox:        d("dr_apply_inbox_batches", "Lotes entregues pelo CommitPump e ainda não despachados pelo applier."),
		commitUndelivered: d("dr_commit_undelivered", "Entradas commitadas que o CommitPump ainda não entregou ao applier (ficam na memória do Raft, ADR-060)."),
		electionTicks:     d("dr_election_ticks", "ELECTION_TICKS: timeout de eleição em ticks de 100 ms."),

		memBytes:      d("dr_raft_mem_bytes", "Bytes das entradas no MemoryStorage do Raft."),
		memEntries:    d("dr_raft_mem_entries", "Entradas no MemoryStorage do Raft."),
		memMax:        d("dr_raft_mem_max_bytes", "RAFT_RAM_MAX: teto da RAM do Raft (DC-14)."),
		memPeer:       d("dr_raft_mem_peer_bytes", "Bytes da RAM do Raft desde o Match do par: o que a líder segura por ele. Só na líder.", "par"),
		snapshotIndex: d("dr_raft_snapshot_index", "Índice do último snapshot-marcador."),
		behindTail:    d("dr_raft_behind_tail", "1 se a célula recebeu um marcador à frente do aplicado e espera a recuperação."),

		replApplied:     d("dr_repl_applied_index", "Aplicado de cada célula conhecido pela líder (a própria líder incluída): a versão _dr_v garantida no banco. Só na líder.", "par"),
		replKnown:       d("dr_repl_known", "1 se a líder tem notícia do aplicado da célula. Só na líder.", "par"),
		replAge:         d("dr_repl_report_age_seconds", "Idade da última notícia do aplicado da célula. Só na líder.", "par"),
		replLag:         d("dr_repl_lag_entries", "Entradas commitadas que a célula ainda não tem no banco. Só na líder.", "par"),
		replDelay:       d("dr_repl_delay_seconds", "Atraso de replicação: idade, no relógio da líder, da entrada commitada mais antiga que a célula não tem no banco (M-11). Só na líder.", "par"),
		replSaturated:   d("dr_repl_delay_saturated", "1 se o atraso passou do horizonte das amostras (o valor é um mínimo). Só na líder.", "par"),
		replSynced:      d("dr_repl_synced", "1 se a célula está dentro de REPL_SYNC_MAX (DC-13). Só na líder.", "par"),
		recoveryOn:      d("dr_recovery_in_progress", "1 se a célula está numa sessão de recuperação (DC-17). Só na líder.", "par"),
		recoveryElapsed: d("dr_recovery_elapsed_seconds", "Duração da sessão de recuperação em curso (0 sem sessão). Só na líder.", "par"),
		behindLeaderRAM: d("dr_repl_behind_leader_ram", "1 se o par está atrás da primeira entrada na RAM da líder: volta pelo CatchUp ou pela reconstrução. Só na líder.", "par"),
		replSyncMax:     d("dr_repl_sync_max_seconds", "REPL_SYNC_MAX: atraso máximo de uma célula sincronizada (DC-13). Só na líder."),
		replHorizon:     d("dr_repl_clock_horizon_seconds", "Horizonte das amostras do relógio de replicação (DC-16). Só na líder."),
		walRetained:     d("dr_wal_retained_bytes", "Bytes dos arquivos .wal retidos no disco do log do Raft (RT-15)."),
		walFiles:        d("dr_wal_files", "Arquivos .wal retidos no disco do log do Raft (RT-15)."),
		retentionFloor:  d("dr_retention_floor", "Piso de retenção do disco: calculado na líder, recebido na follower (RT-15)."),
		walPins:         d("dr_wal_pins", "Pinos de retenção ativos (leituras do disco, CatchUp e reconstrução)."),
		cellState:       d("dr_cell_state", "1 no estado atual da célula diante do log (normal, recuperando, reconstruindo; AP-16).", "estado"),
	}
}

func (c *StateCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.isLeader, c.leaderID, c.term, c.commit, c.gate, c.applied,
		c.quorumActive, c.quorumVoters, c.quorumOk, c.peerMatch, c.peerLag,
		c.applierStopped, c.ready, c.applyWorkers, c.applyInFlight,
		c.applyLag, c.applyLagMax, c.applyInbox, c.commitUndelivered, c.electionTicks,
		c.memBytes, c.memEntries, c.memMax, c.memPeer, c.snapshotIndex, c.behindTail,
		c.replApplied, c.replKnown, c.replAge, c.replLag, c.replDelay, c.replSaturated, c.replSynced,
		c.recoveryOn, c.recoveryElapsed, c.behindLeaderRAM, c.replSyncMax, c.replHorizon,
		c.walRetained, c.walFiles, c.retentionFloor, c.walPins, c.cellState} {
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
	g(c.applied, float64(st.Aplicado))
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
	g(c.applyWorkers, float64(st.Apply.Workers))
	g(c.applyInFlight, float64(st.Apply.EmVoo))
	g(c.applyLag, float64(st.Apply.Atraso))
	g(c.applyLagMax, float64(st.Apply.AtrasoMax))
	g(c.applyInbox, float64(st.Apply.Fila))
	g(c.commitUndelivered, float64(st.CommitNaoEntregue))
	g(c.electionTicks, float64(st.ElectionTicks))

	m := st.Raft.Memoria
	g(c.memBytes, float64(m.Bytes))
	g(c.memEntries, float64(m.Entradas))
	g(c.memMax, float64(m.Teto))
	g(c.snapshotIndex, float64(st.Raft.Snapshot))
	g(c.behindTail, b2f(st.Raft.AtrasCauda))
	for par, b := range m.PorPar {
		g(c.memPeer, float64(b), par)
	}
	g(c.walRetained, float64(st.Wal.Retido.Bytes))
	g(c.walFiles, float64(st.Wal.Retido.Arquivos))
	g(c.retentionFloor, float64(st.Wal.Piso))
	g(c.walPins, float64(st.Wal.Pinos))
	for _, e := range CellStates {
		g(c.cellState, b2f(st.Modo.Estado == e), e)
	}
	if r := st.Replicacao; r != nil {
		g(c.replSyncMax, r.SyncMaxSeg)
		g(c.replHorizon, r.HorizonteSeg)
		for par, cl := range r.Celulas {
			g(c.replApplied, float64(cl.Aplicado), par)
			g(c.replKnown, b2f(cl.Conhecido), par)
			g(c.replAge, cl.IdadeNoticiaSeg, par)
			g(c.replLag, float64(cl.AtrasoEntradas), par)
			g(c.replDelay, cl.AtrasoSeg, par)
			g(c.replSaturated, b2f(cl.Saturado), par)
			g(c.replSynced, b2f(cl.Sincronizada), par)
			g(c.recoveryOn, b2f(cl.Recuperando), par)
			g(c.recoveryElapsed, cl.RecuperandoHaSeg, par)
			g(c.behindLeaderRAM, b2f(cl.AtrasCaudaLider), par)
		}
	}
}

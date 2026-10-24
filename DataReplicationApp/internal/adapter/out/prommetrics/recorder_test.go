package prommetrics

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"example.com/datareplication/internal/usecase"
)

type fakeSource struct {
	st    usecase.CellStatus
	ready bool
}

func (f fakeSource) Status() usecase.CellStatus { return f.st }
func (f fakeSource) Ready() (bool, string)      { return f.ready, "" }

func leaderSource() fakeSource {
	return fakeSource{ready: true, st: usecase.CellStatus{
		Celula: "1", No: 1, Papel: "lider", Lider: 1, Termo: 3, Commit: 10, Gate: 10, Aplicado: 9,
		Match:  map[string]uint64{"1": 10, "2": 10, "3": 7},
		Quorum: &usecase.QuorumStatus{Ativos: 2, Votantes: 3, Ok: true},
		Raft: usecase.RaftLogStatus{Snapshot: 8, Memoria: usecase.MemoriaStatus{Entradas: 4, Bytes: 4096, Teto: 1 << 20,
			Primeiro: 6, PorPar: map[string]int64{"2": 0, "3": 2048}}},
		Replicacao: &usecase.ReplicacaoStatus{SyncMaxSeg: 2, HorizonteSeg: 30, Celulas: map[string]usecase.ReplicacaoCelula{
			"1": {Aplicado: 9, Conhecido: true, AtrasoEntradas: 1, AtrasoSeg: 0.01, Sincronizada: true},
			"2": {Aplicado: 10, Conhecido: true, Sincronizada: true},
			"3": {Aplicado: 4, Conhecido: true, IdadeNoticiaSeg: 0.1, AtrasoEntradas: 6, AtrasoSeg: 7.5, Recuperando: true,
				RecuperandoHaSeg: 5, AtrasCaudaLider: true, MemoriaBytes: 2048},
		}},
	}}
}

// Cada método de port.Metrics muda a série certa.
func TestRecorderEvents(t *testing.T) {
	r := NewRecorder("1", nil)
	r.LeaderChanged(2)
	r.RaftMessageSent("heartbeat")
	r.RaftMessageSent("heartbeat")
	r.RaftMessageReceived("app_resp")
	r.HeartbeatSeen()
	r.WALFsync(2 * time.Millisecond)
	r.PeerSendDropped(3)
	r.Forwarded()
	r.WriteDone("ok", 5*time.Millisecond)
	r.WriteDone("no_leader", time.Second)
	r.ReadDone("get", "ok")
	r.ReadBarrier(time.Millisecond)
	r.Throttled("atraso_apply")
	r.Throttled("atraso_apply")
	r.HTTPRequest("PutItem", 400, time.Millisecond)
	r.HTTPRequest("PutItem", 200, time.Millisecond)
	r.HTTPInFlight(1)
	r.HTTPInFlight(1)
	r.HTTPInFlight(-1)
	r.RAMCeilingHit()
	r.RecoveryFinished(3, 4*time.Second)
	r.ReplicationStage("banco_todas", 20*time.Millisecond)

	checks := []struct {
		name      string
		got, want float64
	}{
		{"trocas de líder", testutil.ToFloat64(r.leaderChanges), 1},
		{"heartbeats enviados", testutil.ToFloat64(r.msgSent.WithLabelValues("heartbeat")), 2},
		{"app_resp recebidos", testutil.ToFloat64(r.msgReceived.WithLabelValues("app_resp")), 1},
		{"descartes para o par 3", testutil.ToFloat64(r.sendDropped.WithLabelValues("3")), 1},
		{"encaminhamentos", testutil.ToFloat64(r.forwards), 1},
		{"escritas ok", testutil.ToFloat64(r.writes.WithLabelValues("ok")), 1},
		{"escritas no_leader", testutil.ToFloat64(r.writes.WithLabelValues("no_leader")), 1},
		{"escritas pending", testutil.ToFloat64(r.writes.WithLabelValues("pending")), 0},
		{"leituras get ok", testutil.ToFloat64(r.reads.WithLabelValues("get", "ok")), 1},
		{"contrapressão por atraso do apply", testutil.ToFloat64(r.throttled.WithLabelValues("atraso_apply")), 2},
		{"contrapressão por buffer cheio", testutil.ToFloat64(r.throttled.WithLabelValues("buffer_cheio")), 0},
		{"HTTP PutItem 400", testutil.ToFloat64(r.httpRequests.WithLabelValues("PutItem", "400")), 1},
		{"HTTP PutItem 200", testutil.ToFloat64(r.httpRequests.WithLabelValues("PutItem", "200")), 1},
		{"HTTP em andamento", testutil.ToFloat64(r.httpInFlight), 1},
		{"teto da RAM atingido", testutil.ToFloat64(r.ramCeiling), 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, quer %v", c.name, c.got, c.want)
		}
	}
	out := scrape(t, r)
	for _, want := range []string{
		`dr_recovery_duration_seconds_count{celula="1",par="3"} 1`,
		`dr_replication_duration_seconds_count{celula="1",etapa="banco_todas"} 1`,
		`dr_replication_duration_seconds_count{celula="1",etapa="commit"} 0`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("faltou %q", want)
		}
	}
	if hb := testutil.ToFloat64(r.lastHeartbeat); time.Since(time.Unix(int64(hb), 0)) > 5*time.Second {
		t.Fatalf("último heartbeat = %v", hb)
	}
	// Só o ACK ok entra no histograma de latência.
	if n := testutil.CollectAndCount(r.writeDuration); n != 1 {
		t.Fatalf("histograma de escrita com %d séries", n)
	}
}

// O coletor de estado exporta líder, quórum e pares a partir do retrato do /status.
func TestStateCollectorLeaderAndFollower(t *testing.T) {
	r := NewRecorder("1", leaderSource())
	out := scrape(t, r)
	for _, want := range []string{
		`dr_is_leader{celula="1"} 1`, `dr_leader_id{celula="1"} 1`, `dr_raft_term{celula="1"} 3`,
		`dr_commit_index{celula="1"} 10`, `dr_applied_index{celula="1"} 9`,
		`dr_quorum_active{celula="1"} 2`, `dr_quorum_ok{celula="1"} 1`, `dr_quorum_voters{celula="1"} 3`,
		`dr_peer_lag{celula="1",par="3"} 3`, `dr_peer_match_index{celula="1",par="2"} 10`,
		`dr_ready{celula="1"} 1`,
		`dr_applier_stopped{celula="1"} 0`,
		// O-51: RAM do Raft e replicação até o banco (DC-13, DC-14, DC-17)
		`dr_raft_mem_bytes{celula="1"} 4096`, `dr_raft_mem_max_bytes{celula="1"} 1.048576e+06`,
		`dr_raft_mem_peer_bytes{celula="1",par="3"} 2048`, `dr_raft_snapshot_index{celula="1"} 8`,
		`dr_repl_applied_index{celula="1",par="1"} 9`, `dr_repl_applied_index{celula="1",par="3"} 4`,
		`dr_repl_lag_entries{celula="1",par="3"} 6`, `dr_repl_delay_seconds{celula="1",par="3"} 7.5`,
		`dr_repl_synced{celula="1",par="3"} 0`, `dr_repl_synced{celula="1",par="2"} 1`,
		`dr_recovery_in_progress{celula="1",par="3"} 1`, `dr_recovery_elapsed_seconds{celula="1",par="3"} 5`,
		`dr_repl_behind_leader_ram{celula="1",par="3"} 1`, `dr_repl_sync_max_seconds{celula="1"} 2`,
		`dr_repl_clock_horizon_seconds{celula="1"} 30`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("faltou %q", want)
		}
	}
	if strings.Contains(out, `dr_peer_lag{celula="1",par="1"}`) || strings.Contains(out, `dr_peer_match_index{celula="1",par="1"}`) {
		t.Error("a própria líder não pode aparecer como par do log")
	}

	f := fakeSource{st: usecase.CellStatus{Celula: "2", No: 2, Papel: "follower", Lider: 1, ErroApply: "parado em (5, 1): x"}}
	out = scrape(t, NewRecorder("2", f))
	for _, want := range []string{`dr_is_leader{celula="2"} 0`, `dr_applier_stopped{celula="2"} 1`, `dr_ready{celula="2"} 0`} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("follower: faltou %q", want)
		}
	}
	for _, absent := range []string{"dr_quorum_ok{", "dr_peer_lag{", "dr_repl_", "dr_recovery_in_progress{", "dr_raft_mem_peer_bytes{"} {
		if strings.Contains(out, absent) {
			t.Errorf("follower não exporta %s", absent)
		}
	}
}

func scrape(t *testing.T, r *Recorder) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

// registeredNames devolve todas as séries dr_* do registro (com líder, para incluir quórum e pares).
func registeredNames(t *testing.T) map[string]bool {
	t.Helper()
	r := NewRecorder("1", leaderSource())
	r.PeerSendDropped(2)
	r.WriteDone("ok", time.Millisecond)
	r.WALFsync(time.Millisecond)
	r.ReadBarrier(time.Millisecond)
	r.RecoveryFinished(3, time.Second)
	mfs, err := r.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	return names
}

// Todas as séries da O-15 existem desde a partida.
func TestAllSeriesRegistered(t *testing.T) {
	names := registeredNames(t)
	for _, n := range []string{
		"dr_leader_changes_total", "dr_raft_messages_sent_total", "dr_raft_messages_received_total",
		"dr_raft_last_heartbeat_timestamp_seconds", "dr_wal_fsync_duration_seconds", "dr_peer_send_dropped_total",
		"dr_forwards_total", "dr_writes_total", "dr_write_duration_seconds", "dr_reads_total",
		"dr_read_barrier_duration_seconds", "dr_is_leader", "dr_leader_id", "dr_raft_term", "dr_commit_index",
		"dr_gate_index", "dr_applied_index",
		"dr_quorum_active", "dr_quorum_voters", "dr_quorum_ok", "dr_peer_match_index", "dr_peer_lag",
		"dr_applier_stopped", "dr_ready", "dr_apply_workers", "dr_apply_inflight",
		"dr_apply_item_duration_seconds",
		// ADR-060 (O-27, O-28)
		"dr_raft_loop_duration_seconds", "dr_raft_tick_gap_seconds", "dr_throttled_total",
		"dr_http_requests_total", "dr_http_request_duration_seconds", "dr_http_inflight",
		"dr_apply_lag", "dr_apply_lag_max", "dr_apply_inbox_batches", "dr_commit_undelivered", "dr_election_ticks",
		// Compactação do log, Incremento A (O-37, item 6)
		"dr_raft_snapshot_duration_seconds", "dr_raft_snapshot_deferred_total", "dr_startup_duration_seconds",
		// Observabilidade da replicação (O-51)
		"dr_raft_mem_bytes", "dr_raft_mem_entries", "dr_raft_mem_max_bytes", "dr_raft_mem_peer_bytes",
		"dr_raft_snapshot_index", "dr_raft_behind_tail", "dr_raft_mem_ceiling_total",
		"dr_repl_applied_index", "dr_repl_known", "dr_repl_report_age_seconds", "dr_repl_lag_entries",
		"dr_repl_delay_seconds", "dr_repl_delay_saturated", "dr_repl_synced", "dr_repl_behind_leader_ram",
		"dr_repl_sync_max_seconds", "dr_repl_clock_horizon_seconds",
		"dr_recovery_in_progress", "dr_recovery_elapsed_seconds",
		"dr_replication_duration_seconds", "dr_recovery_duration_seconds",
	} {
		if !names[n] {
			t.Errorf("série %s não registrada", n)
		}
	}
}

// Contrato com os dashboards: toda métrica dr_* citada em cada JSON do Grafana em
// observability/grafana/dashboards existe no registro.
func TestDashboardOnlyUsesExistingMetrics(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "observability", "grafana", "dashboards", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("nenhum dashboard encontrado: %v", err)
	}
	names := registeredNames(t)
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		used := map[string]bool{}
		for _, m := range regexp.MustCompile(`dr_[a-z_]+`).FindAllString(string(b), -1) {
			for _, suf := range []string{"_bucket", "_sum", "_count"} {
				if strings.HasSuffix(m, suf) && names[strings.TrimSuffix(m, suf)] {
					m = strings.TrimSuffix(m, suf)
				}
			}
			used[m] = true
		}
		var missing []string
		for m := range used {
			if !names[m] {
				missing = append(missing, m)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s consulta séries que não existem: %v", filepath.Base(path), missing)
		}
		if len(used) < 20 {
			t.Errorf("%s cita só %d séries dr_*; esperado o conjunto da O-18", filepath.Base(path), len(used))
		}
	}
}

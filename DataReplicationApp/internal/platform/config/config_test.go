package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func valid() map[string]string {
	return map[string]string{
		"CELL_ID": "a",
		"NODE_ID": "1", "PEERS": "1=cell-1:7000,2=cell-2:7000,3=cell-3:7000",
		"TABLES": "cadastro_cliente_cell_{cell}", "AWS_REGION": "sa-east-1",
		"CLUSTER_ID": "poc-local",
	}
}

func TestLoadValid(t *testing.T) {
	c, err := LoadFrom(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.RequestDeadline != 7500*time.Millisecond || c.WaitLeader != time.Second {
		t.Fatalf("padrões inesperados: %+v", c)
	}
}

// CELL_ID não tem padrão: ausência derruba a partida.
func TestMandatoryVars(t *testing.T) {
	for _, k := range []string{"CELL_ID"} {
		m := valid()
		delete(m, k)
		_, err := LoadFrom(env(m))
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("sem %s devia falhar citando a variável, veio %v", k, err)
		}
	}
}

func TestInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"deadline":   {"REQUEST_DEADLINE": "30s"},
		"forward":    {"FORWARD_TIMEOUT": "8s"},
		"waitleader": {"WAIT_LEADER": "4s"},
		"idle":       {"IDLE_TIMEOUT": "60s"},
	}
	for name, over := range cases {
		m := valid()
		for k, v := range over {
			m[k] = v
		}
		if _, err := LoadFrom(env(m)); err == nil {
			t.Errorf("%s: devia falhar", name)
		}
	}
}

// Modo único strong (ADR-057): manifesto antigo com variável de modo falha em vez de ser ignorado.
func TestModeVarsRemoved(t *testing.T) {
	for _, k := range RemovedModeVars {
		m := valid()
		m[k] = "eventual"
		_, err := LoadFrom(env(m))
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s devia ser recusada, veio %v", k, err)
		}
	}
}

func TestErrorsAreJoined(t *testing.T) {
	_, err := LoadFrom(env(map[string]string{}))
	if err == nil {
		t.Fatal("esperava erro")
	}
	for _, k := range []string{"CELL_ID", "NODE_ID"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("erro agregado sem %s: %v", k, err)
		}
	}
}

func TestClusterVars(t *testing.T) {
	c, err := LoadFrom(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.NodeID != 1 || len(c.Peers) != 3 || c.Peers[2] != "cell-2:7000" {
		t.Fatalf("membros: %+v", c)
	}
	if c.RaftDir != "/var/lib/dr/raft/wal" || c.PeerAddr != ":7000" ||
		c.TableWait != 120*time.Second || c.GatePublishInterval != 100*time.Millisecond {
		t.Fatalf("padrões: %+v", c)
	}
	if ids := IDs(c.Peers); len(ids) != 3 || ids[0] != 1 || ids[2] != 3 {
		t.Fatalf("IDs = %v", ids)
	}
	bad := []struct {
		key, val, want string
	}{
		{"NODE_ID", "0", "NODE_ID"},
		{"NODE_ID", "", "NODE_ID"},
		{"NODE_ID", "9", "não contém o NODE_ID"},
		{"PEERS", "1=a,1=b", "repetido"},
		{"PEERS", "x", "PEERS inválida"},
		{"TABLES", "sem_marcador", "{cell}"},
		{"TABLES", "", "TABLES"},
		{"AWS_REGION", "", "AWS_REGION"},
	}
	for _, b := range bad {
		m := valid()
		m[b.key] = b.val
		if _, err := LoadFrom(env(m)); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s=%q: erro %v, quer %q", b.key, b.val, err, b.want)
		}
	}
}

// APPLY_WORKERS (ADR-058): padrão 16, de 1 a MaxApplyWorkers.
func TestApplyWorkers(t *testing.T) {
	c, err := LoadFrom(env(valid()))
	if err != nil || c.ApplyWorkers != 16 {
		t.Fatalf("padrão = %d, %v; quer 16", c.ApplyWorkers, err)
	}
	for v, want := range map[string]int{"1": 1, "64": 64, "256": 256} {
		m := valid()
		m["APPLY_WORKERS"] = v
		if c, err := LoadFrom(env(m)); err != nil || c.ApplyWorkers != want {
			t.Errorf("APPLY_WORKERS=%s → %d, %v", v, c.ApplyWorkers, err)
		}
	}
	for _, v := range []string{"0", "-1", "257", "muitos"} {
		m := valid()
		m["APPLY_WORKERS"] = v
		if _, err := LoadFrom(env(m)); err == nil || !strings.Contains(err.Error(), "APPLY_WORKERS") {
			t.Errorf("APPLY_WORKERS=%s devia falhar, veio %v", v, err)
		}
	}
}

// ADR-060: padrões e limites de APPLY_LAG_MAX e ELECTION_TICKS.
func TestLoadADR060Vars(t *testing.T) {
	c, err := LoadFrom(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ApplyLagMax != 16*256 || c.ElectionTicks != 10 {
		t.Fatalf("padrões inesperados: lag %d ticks %d", c.ApplyLagMax, c.ElectionTicks)
	}
	m := valid()
	m["APPLY_WORKERS"], m["ELECTION_TICKS"] = "4", "30"
	c, err = LoadFrom(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.ApplyLagMax != 4*256 || c.ElectionTicks != 30 {
		t.Fatalf("valores lidos errados: %+v", c)
	}
	m["APPLY_LAG_MAX"] = "50"
	if c, err = LoadFrom(env(m)); err != nil || c.ApplyLagMax != 50 {
		t.Fatalf("APPLY_LAG_MAX=50: %d %v", c.ApplyLagMax, err)
	}
	for k, bad := range map[string]string{"APPLY_LAG_MAX": "0", "ELECTION_TICKS": "2"} {
		m := valid()
		m[k] = bad
		if _, err := LoadFrom(env(m)); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%s devia falhar citando a variável, veio %v", k, bad, err)
		}
	}
}

// O-30: padrões das variáveis da compactação (S.6 da trecho).
func TestCompactionDefaults(t *testing.T) {
	c, err := LoadFrom(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ClusterID != "poc-local" || c.SnapshotEveryEntries != 10_000 || c.SnapshotEveryBytes != 64<<20 ||
		c.RaftCatchupEntries != 5_000 || c.RaftRAMMax != 256<<20 || c.RaftCompactInterval != time.Second ||
		c.RetainMaxBytes != 2<<30 || c.RetainDiskHard != 0.85 || c.RetainDiskHardBytes != 0 ||
		c.WalSegmentBytes != 64_000_000 || c.RebuildRetry != time.Minute || c.RebuildPinTTL != 5*time.Minute ||
		c.PeerQueueBytes != 64<<20 {
		t.Fatalf("padrões da compactação inesperados: %+v", c)
	}
}

// O-30: os valores de teste da trecho (S.6) são aceitos, com sufixos.
func TestCompactionTestValues(t *testing.T) {
	m := valid()
	for k, v := range map[string]string{
		"SNAPSHOT_EVERY_ENTRIES": "200", "RAFT_CATCHUP_ENTRIES": "50", "WAL_SEGMENT_BYTES": "1MiB",
		"RETAIN_MAX_BYTES": "4MiB", "RETAIN_DISK_HARD_BYTES": "12MiB", "REBUILD_RETRY": "20s",
	} {
		m[k] = v
	}
	c, err := LoadFrom(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.SnapshotEveryEntries != 200 || c.RaftCatchupEntries != 50 || c.WalSegmentBytes != 1<<20 ||
		c.RetainMaxBytes != 4<<20 || c.RetainDiskHardBytes != 12<<20 || c.RebuildRetry != 20*time.Second {
		t.Fatalf("valores de teste não lidos: %+v", c)
	}
}

// O-30: cada faixa (abaixo, acima, formato) e cada regra de coerência recusa, citando a variável.
func TestCompactionInvalid(t *testing.T) {
	cases := []struct {
		name string
		over map[string]string
		want string
	}{
		{"cluster ausente", map[string]string{"CLUSTER_ID": ""}, "CLUSTER_ID"},
		{"cluster formato", map[string]string{"CLUSTER_ID": "POC_Local"}, "CLUSTER_ID"},
		{"entradas abaixo", map[string]string{"SNAPSHOT_EVERY_ENTRIES": "99"}, "SNAPSHOT_EVERY_ENTRIES"},
		{"entradas acima", map[string]string{"SNAPSHOT_EVERY_ENTRIES": "10000001"}, "SNAPSHOT_EVERY_ENTRIES"},
		{"bytes formato", map[string]string{"SNAPSHOT_EVERY_BYTES": "64XB"}, "SNAPSHOT_EVERY_BYTES"},
		{"bytes acima", map[string]string{"SNAPSHOT_EVERY_BYTES": "2GiB"}, "SNAPSHOT_EVERY_BYTES"},
		{"intervalo abaixo", map[string]string{"RAFT_COMPACT_INTERVAL": "10ms"}, "RAFT_COMPACT_INTERVAL"},
		{"teto fração", map[string]string{"RETAIN_DISK_HARD": "0.99"}, "RETAIN_DISK_HARD"},
		{"segmento abaixo", map[string]string{"WAL_SEGMENT_BYTES": "1KiB"}, "WAL_SEGMENT_BYTES"},
		{"pino abaixo", map[string]string{"REBUILD_PIN_TTL": "1s"}, "REBUILD_PIN_TTL"},
		{"fila acima", map[string]string{"PEER_QUEUE_BYTES": "2GiB"}, "PEER_QUEUE_BYTES"},
		{"catchup >= snapshot", map[string]string{"SNAPSHOT_EVERY_ENTRIES": "200", "RAFT_CATCHUP_ENTRIES": "200"}, "RAFT_CATCHUP_ENTRIES"},
		{"ram pequena", map[string]string{"RAFT_RAM_MAX": "96MiB"}, "RAFT_RAM_MAX"},
		{"teto absoluto <= janela", map[string]string{"RETAIN_MAX_BYTES": "4MiB", "WAL_SEGMENT_BYTES": "1MiB", "RETAIN_DISK_HARD_BYTES": "4MiB"}, "RETAIN_DISK_HARD_BYTES"},
		{"janela < segmento", map[string]string{"RETAIN_MAX_BYTES": "32MiB"}, "RETAIN_MAX_BYTES"},
	}
	for _, tc := range cases {
		m := valid()
		for k, v := range tc.over {
			m[k] = v
		}
		_, err := LoadFrom(env(m))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: devia falhar citando %s, veio %v", tc.name, tc.want, err)
		}
	}
}

// O-30: todos os erros da compactação vêm reunidos numa chamada.
func TestCompactionErrorsJoined(t *testing.T) {
	m := valid()
	m["CLUSTER_ID"] = ""
	m["SNAPSHOT_EVERY_ENTRIES"] = "1"
	m["PEER_QUEUE_BYTES"] = "x"
	_, err := LoadFrom(env(m))
	for _, k := range []string{"CLUSTER_ID", "SNAPSHOT_EVERY_ENTRIES", "PEER_QUEUE_BYTES"} {
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("faltou %s no erro reunido: %v", k, err)
		}
	}
}

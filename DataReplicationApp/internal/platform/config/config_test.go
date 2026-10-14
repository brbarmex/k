package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func valid() map[string]string {
	return map[string]string{
		"CELL_ID":         "a",
		"DEGRADED_POLICY": "accept", "DEGRADED_LEASE": "5m",
		"NODE_ID": "1", "PEERS": "1=cell-1:7000,2=cell-2:7000,3=cell-3:7000",
		"POC_SEM_AZURE": "true", "TABLES": "cadastro_cliente_cell_{cell}", "AWS_REGION": "sa-east-1",
	}
}

func TestLoadValid(t *testing.T) {
	c, err := LoadFrom(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.RequestDeadline != 7500*time.Millisecond || c.WaitLeader != time.Second || c.DegradedLease != 5*time.Minute {
		t.Fatalf("padrões inesperados: %+v", c)
	}
}

// DEGRADED_POLICY e DEGRADED_LEASE não têm padrão: ausência derruba a partida (ADR-045).
func TestDegradedVarsAreMandatory(t *testing.T) {
	for _, k := range []string{"DEGRADED_POLICY", "DEGRADED_LEASE", "CELL_ID"} { // com accept
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
		"politica":                {"DEGRADED_POLICY": "talvez"},
		"lease":                   {"DEGRADED_LEASE": "0s"},
		"lease2":                  {"DEGRADED_LEASE": "abc"},
		"leaseMenorQueRequisicao": {"DEGRADED_LEASE": "5s"},
		"deadline":                {"REQUEST_DEADLINE": "30s"},
		"degrade":                 {"DEGRADE_AFTER": "8s"},
		"forward":                 {"FORWARD_TIMEOUT": "8s"},
		"waitleader":              {"WAIT_LEADER": "4s"},
		"idle":                    {"IDLE_TIMEOUT": "60s"},
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
	for _, k := range []string{"CELL_ID", "DEGRADED_POLICY"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("erro agregado sem %s: %v", k, err)
		}
	}
}

// Com reject não há lease: DEGRADED_LEASE pode faltar e, se vier, é ignorada sem erro.
func TestLeaseOnlyRequiredWithAccept(t *testing.T) {
	m := valid()
	m["DEGRADED_POLICY"] = "reject"
	delete(m, "DEGRADED_LEASE")
	c, err := LoadFrom(env(m))
	if err != nil || c.DegradedLease != 0 {
		t.Fatalf("reject sem DEGRADED_LEASE devia subir: %v %+v", err, c)
	}
	for _, v := range []string{"5m", "lixo", "0s", "1s"} {
		m["DEGRADED_LEASE"] = v
		c, err := LoadFrom(env(m))
		if err != nil || c.DegradedLease != 0 {
			t.Fatalf("reject ignora DEGRADED_LEASE=%q: %v %+v", v, err, c)
		}
	}
	// Mas a política continua obrigatória.
	delete(m, "DEGRADED_POLICY")
	if _, err := LoadFrom(env(m)); err == nil {
		t.Fatal("DEGRADED_POLICY segue obrigatória")
	}
}

func TestClusterVars(t *testing.T) {
	c, err := LoadFrom(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.NodeID != 1 || len(c.Peers) != 3 || c.Peers[2] != "cell-2:7000" || len(c.Learners) != 0 || !c.POCNoAzure {
		t.Fatalf("membros: %+v", c)
	}
	if c.RaftDir != "/var/lib/dr/raft/wal" || c.ReplogDir != "/var/lib/dr/replog" || c.PeerAddr != ":7000" ||
		c.TableWait != 120*time.Second || c.GatePublishInterval != 100*time.Millisecond || c.ReplogSyncInterval != time.Second {
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
		{"POC_SEM_AZURE", "", "LEARNERS vazia só é aceita na POC"},
		{"LEARNERS", "4=a:1", "3 learners"},
		{"LEARNERS", "1=a:1,5=b:1,6=c:1", "PEERS e em LEARNERS"},
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
	m := valid()
	m["LEARNERS"] = "4=az-1:7000,5=az-2:7000,6=az-3:7000"
	delete(m, "POC_SEM_AZURE")
	if c, err := LoadFrom(env(m)); err != nil || len(c.Learners) != 3 {
		t.Fatalf("com learners: %v %+v", err, c.Learners)
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

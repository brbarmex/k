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
		"POC_SEM_AZURE": "true", "TABLES": "cadastro_cliente_cell_{cell}", "AWS_REGION": "sa-east-1",
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
	if c.NodeID != 1 || len(c.Peers) != 3 || c.Peers[2] != "cell-2:7000" || len(c.Learners) != 0 || !c.POCNoAzure {
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

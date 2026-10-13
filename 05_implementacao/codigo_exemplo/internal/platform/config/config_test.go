package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func valid() map[string]string {
	return map[string]string{
		"CELL_ID": "a", "READ_MODE_DEFAULT": "strong",
		"DEGRADED_POLICY": "accept", "DEGRADED_LEASE": "5m",
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
	for _, k := range []string{"DEGRADED_POLICY", "DEGRADED_LEASE", "CELL_ID", "READ_MODE_DEFAULT"} { // com accept
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
		"readmode":                {"READ_MODE_DEFAULT": "quase"},
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

// WRITE_MODE saiu da v1 (ADR-053): manifesto antigo falha em vez de ser ignorado.
func TestWriteModeRemoved(t *testing.T) {
	m := valid()
	m["WRITE_MODE"] = "eventual"
	_, err := LoadFrom(env(m))
	if err == nil || !strings.Contains(err.Error(), "WRITE_MODE") {
		t.Fatalf("WRITE_MODE devia ser recusada, veio %v", err)
	}
}

func TestErrorsAreJoined(t *testing.T) {
	_, err := LoadFrom(env(map[string]string{}))
	if err == nil {
		t.Fatal("esperava erro")
	}
	for _, k := range []string{"CELL_ID", "READ_MODE_DEFAULT", "DEGRADED_POLICY"} {
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

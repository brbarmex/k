package domain

import "testing"

func TestRaftCompactLimitNeverPassesDurable(t *testing.T) {
	// Copiado até 100, mas o último fsync do log de replicação cobriu só até 60.
	if got := RaftCompactLimit(60, 100, 1000); got != 60 {
		t.Fatalf("limite = %d, quer 60 (nunca além do durável)", got)
	}
}

func TestRaftCompactLimitHoldsForSlowPeerWithinMargin(t *testing.T) {
	if got := RaftCompactLimit(100, 90, 50); got != 90 {
		t.Fatalf("limite = %d, quer 90 (par atrasado dentro da folga)", got)
	}
}

func TestRaftCompactLimitIgnoresPeerBeyondMargin(t *testing.T) {
	// Learner 500 entradas atrás com folga de 50: catch-up pelo log de replicação.
	if got := RaftCompactLimit(1000, 500, 50); got != 1000 {
		t.Fatalf("limite = %d, quer 1000 (par fora da folga não segura o Raft)", got)
	}
}

func TestReplogTruncateLimit(t *testing.T) {
	cases := []struct {
		name                    string
		applied, azure, rebuild Index
		inWindow                bool
		want                    Index
	}{
		{"Azure segura", 100, 40, 0, true, 40},
		{"applied segura", 30, 40, 0, true, 30},
		{"Azure fora da janela não segura", 100, 40, 0, false, 100},
		{"rebuild retém desde F", 100, 90, 20, true, 19},
	}
	for _, c := range cases {
		if got := ReplogTruncateLimit(c.applied, c.azure, c.inWindow, c.rebuild); got != c.want {
			t.Errorf("%s: limite = %d, quer %d", c.name, got, c.want)
		}
	}
}

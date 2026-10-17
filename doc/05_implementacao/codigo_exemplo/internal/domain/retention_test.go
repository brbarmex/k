package domain

import "testing"

func TestRaftCompactLimit(t *testing.T) {
	cases := []struct {
		name                    string
		applied, azure, rebuild Index
		inWindow                bool
		peer                    Index
		retainMax               uint64
		want                    Index
	}{
		{"Azure segura", 100, 40, 0, true, 100, 0, 40},
		{"aplicado segura", 30, 40, 0, true, 100, 0, 30},
		{"Azure fora da janela não segura", 100, 40, 0, false, 100, 0, 100},
		{"reconstrução retém desde F", 100, 90, 20, true, 100, 0, 19},
		{"votante atrasado dentro da folga", 100, 100, 0, true, 90, 50, 90},
		{"votante atrasado fora da folga não segura", 1000, 1000, 0, true, 500, 50, 1000},
	}
	for _, c := range cases {
		if got := RaftCompactLimit(c.applied, c.azure, c.inWindow, c.rebuild, c.peer, c.retainMax); got != c.want {
			t.Errorf("%s: limite = %d, quer %d", c.name, got, c.want)
		}
	}
}

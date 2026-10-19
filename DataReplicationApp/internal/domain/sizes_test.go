package domain

import "testing"

// O-31: tamanhos com e sem sufixo; negativos, sufixos desconhecidos e estouro recusados.
func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0", 0, true}, {"1048576", 1 << 20, true}, {"64MiB", 64 << 20, true}, {"2GiB", 2 << 30, true},
		{"1TiB", 1 << 40, true}, {"4KiB", 4 << 10, true}, {"64MB", 64_000_000, true}, {"1KB", 1000, true},
		{"1GB", 1_000_000_000, true}, {" 8 MiB ", 8 << 20, true},
		{"", 0, false}, {"-1", 0, false}, {"64XB", 0, false}, {"MiB", 0, false}, {"1.5GiB", 0, false},
		{"9223372036854775807KiB", 0, false},
	} {
		got, err := ParseSize(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ParseSize(%q) = %d, %v; quer %d, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

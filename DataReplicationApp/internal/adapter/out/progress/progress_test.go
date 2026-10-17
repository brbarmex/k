package progress

import (
	"os"
	"path/filepath"
	"testing"
)

func mustOpen(t *testing.T, path string) *File {
	t.Helper()
	p, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProgressRoundTripAndCorruptRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress")
	p := mustOpen(t, path)
	if idx, ord, err := p.LoadProgress(); err != nil || idx != 0 || ord != 0 {
		t.Fatalf("progresso inicial: %d %d %v", idx, ord, err)
	}
	if err := p.SaveProgress(42, 3); err != nil {
		t.Fatal(err)
	}
	p.Close()
	p = mustOpen(t, path)
	if idx, ord, _ := p.LoadProgress(); idx != 42 || ord != 3 {
		t.Fatalf("progresso = (%d, %d), quer (42, 3)", idx, ord)
	}
	p.Close()
	b, _ := os.ReadFile(path)
	b[0] ^= 0xFF
	_ = os.WriteFile(path, b, 0o644)
	p = mustOpen(t, path)
	defer p.Close()
	if idx, _, _ := p.LoadProgress(); idx != 0 {
		t.Fatalf("progresso corrompido deve virar (0, 0), veio %d", idx)
	}
}

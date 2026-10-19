package progress

import (
	"os"
	"path/filepath"
	"testing"

	"example.com/datareplication/internal/domain"
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

// O-32, AP-06: Sync concorrente com SaveProgress (-race); o durável nunca recua e só
// conta o que foi gravado antes do fsync.
func TestSyncConcurrentAndMonotonic(t *testing.T) {
	p := mustOpen(t, filepath.Join(t.TempDir(), "progress"))
	defer p.Close()
	if d, err := p.Sync(); err != nil || d != 0 {
		t.Fatalf("Sync sem gravação = %d, %v", d, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 2000; i++ {
			if err := p.SaveProgress(domain.Index(i), 1); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	var last domain.Index
	for {
		d, err := p.Sync()
		if err != nil {
			t.Fatal(err)
		}
		if d < last {
			t.Fatalf("durável recuou de %d para %d", last, d)
		}
		last = d
		select {
		case <-done:
			if d, _ := p.Sync(); d != 2000 || p.Durable() != 2000 {
				t.Fatalf("durável final = %d, quer 2000", d)
			}
			return
		default:
		}
	}
}

// O-32: o registro lido na abertura só vira durável depois do primeiro Sync.
func TestReopenedIsNotDurableUntilSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress")
	p := mustOpen(t, path)
	_ = p.SaveProgress(77, OrdComplete)
	p.Close()
	p = mustOpen(t, path)
	defer p.Close()
	if p.Durable() != 0 {
		t.Fatalf("durável antes do Sync = %d", p.Durable())
	}
	if d, _ := p.Sync(); d != 77 {
		t.Fatalf("durável depois do Sync = %d, quer 77", d)
	}
	if idx, ord, _ := p.LoadProgress(); idx != 77 || ord != OrdComplete {
		t.Fatalf("progresso = (%d, %d)", idx, ord)
	}
}

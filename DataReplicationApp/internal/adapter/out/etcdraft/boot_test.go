package etcdraft

import (
	"os"
	"path/filepath"
	"testing"
)

// DC-12: o token de partida é gravado, relido igual e um arquivo corrompido é fatal.
func TestBootToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boot-token")
	a, err := LoadBootToken(path)
	if err != nil || len(a) != 32 {
		t.Fatalf("gerar: %q %v", a, err)
	}
	b, err := LoadBootToken(path)
	if err != nil || b != a {
		t.Fatalf("reler: %q %v (quer %q)", b, err, a)
	}
	if err := os.WriteFile(path, []byte("lixo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBootToken(path); err == nil {
		t.Fatal("token corrompido devia ser erro")
	}
	if HasState(filepath.Join(t.TempDir(), "wal")) {
		t.Fatal("diretório vazio não tem estado")
	}
}

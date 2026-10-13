package replog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"example.com/datareplication/internal/domain"
)

var ctx = context.Background()

func entries(from, to domain.Index) []domain.Entry {
	var out []domain.Entry
	for i := from; i <= to; i++ {
		out = append(out, domain.Entry{Index: i, Term: 1, Kind: domain.KindWrite,
			Ops: []domain.Op{{Table: "t_{cell}", Kind: domain.OpPut, Key: []byte("k"), Item: []byte(`{"v":1}`)}}})
	}
	return out
}

func mustOpen(t *testing.T, dir string, seg int64) *Log {
	t.Helper()
	l, err := Open(dir, seg)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestDurableIndexOnlyAdvancesOnSync(t *testing.T) {
	l := mustOpen(t, t.TempDir(), 1<<20)
	defer l.Close()
	if err := l.Append(ctx, entries(1, 10)); err != nil {
		t.Fatal(err)
	}
	if got := l.DurableIndex(); got != 0 {
		t.Fatalf("DurableIndex = %d antes do Sync; copiado não é durável", got)
	}
	if l.LastIndex() != 10 {
		t.Fatalf("LastIndex = %d, quer 10", l.LastIndex())
	}
	d, err := l.Sync(ctx)
	if err != nil || d != 10 || l.DurableIndex() != 10 {
		t.Fatalf("Sync = %d, %v; DurableIndex = %d", d, err, l.DurableIndex())
	}
	// O limite de compactação do Raft acompanha o durável, não o copiado.
	if err := l.Append(ctx, entries(11, 20)); err != nil {
		t.Fatal(err)
	}
	if got := domain.RaftCompactLimit(l.DurableIndex(), l.LastIndex(), 0); got != 10 {
		t.Fatalf("limite do Raft = %d, quer 10", got)
	}
}

func TestAppendIgnoresOverlapAndRejectsGap(t *testing.T) {
	l := mustOpen(t, t.TempDir(), 1<<20)
	defer l.Close()
	if err := l.Append(ctx, entries(5, 8)); err != nil {
		t.Fatal(err)
	}
	// Recuperação: o trecho vindo do Raft sobrepõe o que sobreviveu.
	if err := l.Append(ctx, entries(7, 9)); err != nil || l.LastIndex() != 9 {
		t.Fatalf("sobreposição: %v, cabeça %d", err, l.LastIndex())
	}
	if err := l.Append(ctx, entries(11, 12)); !errors.Is(err, ErrGap) {
		t.Fatalf("buraco aceito: %v", err)
	}
}

func TestReadRangeAcrossSegments(t *testing.T) {
	l := mustOpen(t, t.TempDir(), 300) // segmentos pequenos: força rotação
	defer l.Close()
	if err := l.Append(ctx, entries(1, 30)); err != nil {
		t.Fatal(err)
	}
	if len(l.segs) < 3 {
		t.Fatalf("esperava várias rotações, tem %d segmentos", len(l.segs))
	}
	got, err := l.Read(ctx, 12, 25)
	if err != nil || len(got) != 14 || got[0].Index != 12 || got[13].Index != 25 {
		t.Fatalf("Read: %d entradas, %v", len(got), err)
	}
}

func TestCompactPrefixRemovesWholeSegmentsAndKeepsActive(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir, 300)
	defer l.Close()
	if err := l.Append(ctx, entries(1, 30)); err != nil {
		t.Fatal(err)
	}
	before, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	if err := l.CompactPrefix(ctx, 15); err != nil {
		t.Fatal(err)
	}
	after, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	if len(after) >= len(before) {
		t.Fatalf("nenhum segmento apagado: %d -> %d", len(before), len(after))
	}
	got, _ := l.Read(ctx, 16, 30) // o que passa de upTo continua legível
	if len(got) != 15 {
		t.Fatalf("depois da compactação, Read(16,30) = %d entradas", len(got))
	}
	if err := l.CompactPrefix(ctx, 1000); err != nil || len(l.segs) != 1 {
		t.Fatalf("o segmento ativo não pode ser apagado: %v, %d", err, len(l.segs))
	}
}

func TestTornTailIsTruncatedOnOpen(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir, 1<<20)
	if err := l.Append(ctx, entries(1, 5)); err != nil {
		t.Fatal(err)
	}
	path := l.segs[0].path
	size := l.segs[0].size
	l.Close()
	// Queda no meio da escrita do registro 5: sobra só parte dele.
	if err := os.Truncate(path, size-3); err != nil {
		t.Fatal(err)
	}
	l = mustOpen(t, dir, 1<<20)
	defer l.Close()
	if l.LastIndex() != 4 || l.DurableIndex() != 4 {
		t.Fatalf("depois da cauda rasgada: cabeça %d, durável %d", l.LastIndex(), l.DurableIndex())
	}
	// A entrada 5 volta a partir do log do Raft.
	if err := l.Append(ctx, entries(5, 6)); err != nil || l.LastIndex() != 6 {
		t.Fatalf("recompletar a partir do Raft: %v", err)
	}
}

func TestCorruptionInOlderSegmentRefusesToOpen(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir, 300)
	if err := l.Append(ctx, entries(1, 30)); err != nil {
		t.Fatal(err)
	}
	first := l.segs[0].path
	l.Close()
	b, _ := os.ReadFile(first)
	b[len(b)/2] ^= 0xFF // vira um byte no meio de um segmento que não é o último
	if err := os.WriteFile(first, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, 300); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupção no meio aceita: %v", err)
	}
}

func TestProgressRoundTripAndCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir, 1<<20)
	if idx, ord, err := l.LoadProgress(); err != nil || idx != 0 || ord != 0 {
		t.Fatalf("progresso inicial: %d %d %v", idx, ord, err)
	}
	if err := l.SaveProgress(42, 3); err != nil {
		t.Fatal(err)
	}
	l.Close()
	l = mustOpen(t, dir, 1<<20)
	if idx, ord, _ := l.LoadProgress(); idx != 42 || ord != 3 {
		t.Fatalf("progresso = (%d, %d), quer (42, 3)", idx, ord)
	}
	l.Close()
	p := filepath.Join(dir, progressFile)
	b, _ := os.ReadFile(p)
	b[0] ^= 0xFF
	_ = os.WriteFile(p, b, 0o644)
	l = mustOpen(t, dir, 1<<20)
	defer l.Close()
	if idx, _, _ := l.LoadProgress(); idx != 0 {
		t.Fatalf("progresso corrompido deve virar (0, 0), veio %d", idx)
	}
}

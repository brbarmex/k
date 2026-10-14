package replog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// ADR-059: o fsync do checkpoint roda fora da trava. Com o disco "travado" no meio do
// Sync, o Append continua respondendo, e o DurableIndex publicado só cobre o que já
// estava gravado quando o Sync começou.
func TestAppendIsNotBlockedByCheckpointFsync(t *testing.T) {
	l := mustOpen(t, t.TempDir(), 1<<20)
	defer l.Close()
	if err := l.Append(ctx, entries(1, 5)); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	l.fsync = func(f *os.File) error {
		close(entered)
		<-release
		return f.Sync()
	}
	type res struct {
		d   domain.Index
		err error
	}
	done := make(chan res, 1)
	go func() {
		d, err := l.Sync(ctx)
		done <- res{d, err}
	}()
	<-entered // o Sync está dentro do fsync

	appended := make(chan error, 1)
	go func() { appended <- l.Append(ctx, entries(6, 9)) }()
	select {
	case err := <-appended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o Append ficou preso esperando o fsync do checkpoint")
	}
	if l.LastIndex() != 9 || l.DurableIndex() != 0 {
		t.Fatalf("LastIndex = %d, DurableIndex = %d durante o fsync; quer 9 e 0", l.LastIndex(), l.DurableIndex())
	}
	close(release)
	r := <-done
	if r.err != nil || r.d != 5 {
		t.Fatalf("Sync = %d, %v; quer 5 (o que estava gravado quando o fsync começou)", r.d, r.err)
	}
	l.fsync = (*os.File).Sync
	if d, err := l.Sync(ctx); err != nil || d != 9 {
		t.Fatalf("segundo Sync = %d, %v; quer 9", d, err)
	}
}

// A rotação faz fsync do segmento que sai e o entrega ao Sync, que o fecha; o DurableIndex
// cobre o segmento rotacionado mesmo antes do próximo checkpoint.
func TestRotationSyncsOutgoingSegmentAndSyncClosesIt(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir, 300) // segmentos pequenos: rotaciona a cada poucos registros
	if err := l.Append(ctx, entries(1, 30)); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	nSegs, nRetired, durable := len(l.segs), len(l.retired), l.durable
	l.mu.Unlock()
	if nSegs < 3 || nRetired != nSegs-1 {
		t.Fatalf("segmentos = %d, aposentados = %d; quer >= 3 e todos menos o ativo", nSegs, nRetired)
	}
	if durable == 0 || durable >= 30 {
		t.Fatalf("DurableIndex = %d depois da rotação; quer o fim do último segmento rotacionado", durable)
	}
	if d, err := l.Sync(ctx); err != nil || d != 30 {
		t.Fatalf("Sync = %d, %v", d, err)
	}
	l.mu.Lock()
	nRetired = len(l.retired)
	l.mu.Unlock()
	if nRetired != 0 {
		t.Fatalf("o Sync não fechou os %d segmentos aposentados", nRetired)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2 := mustOpen(t, dir, 300)
	defer l2.Close()
	got, err := l2.Read(ctx, 1, 30)
	if err != nil || len(got) != 30 || got[29].Index != 30 {
		t.Fatalf("depois de reabrir: %d entradas, %v", len(got), err)
	}
}

// Append e Sync em paralelo, com -race: nenhuma entrada perdida nem fora de ordem.
func TestConcurrentAppendAndSync(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir, 4096)
	stop := make(chan struct{})
	syncErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				syncErr <- nil
				return
			default:
			}
			if _, err := l.Sync(ctx); err != nil {
				syncErr <- err
				return
			}
		}
	}()
	for i := domain.Index(1); i <= 400; i += 10 {
		if err := l.Append(ctx, entries(i, i+9)); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-syncErr; err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2 := mustOpen(t, dir, 4096)
	defer l2.Close()
	got, err := l2.Read(ctx, 1, 400)
	if err != nil || len(got) != 400 {
		t.Fatalf("lidas %d entradas, %v; quer 400", len(got), err)
	}
	for i, e := range got {
		if e.Index != domain.Index(i+1) {
			t.Fatalf("posição %d com índice %d", i, e.Index)
		}
	}
}

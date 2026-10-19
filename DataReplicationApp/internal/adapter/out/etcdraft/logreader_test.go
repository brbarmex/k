package etcdraft

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
)

// testSegment é o tamanho de segmento dos testes do etcdraft (N-07: WAL real com 64 KiB).
const testSegment = 64 << 10

// payload é um Data de entrada válido para o toDomain, com cerca de n bytes.
func payload(n int) []byte {
	b, _ := json.Marshal(map[string]any{"Kind": 1, "Pad": strings.Repeat("x", n)})
	return b
}

func ent(idx, term uint64, size int) *raftpb.Entry {
	return &raftpb.Entry{Index: proto.Uint64(idx), Term: proto.Uint64(term), Type: raftpb.EntryNormal.Enum(), Data: payload(size)}
}

func hs(term, commit uint64) *raftpb.HardState {
	return &raftpb.HardState{Term: proto.Uint64(term), Commit: proto.Uint64(commit)}
}

// writeWAL cria um WAL real no formato da RT-20 e devolve o diretório.
func writeWAL(t *testing.T, id uint64, cluster string, steps func(w *wal.WAL)) string {
	t.Helper()
	if err := setSegmentSize(testSegment); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "wal")
	meta, _ := json.Marshal(walMeta{NodeID: id, ClusterID: cluster})
	w, err := wal.Create(zap.NewNop(), dir, meta)
	if err != nil {
		t.Fatal(err)
	}
	cs := &raftpb.ConfState{Voters: []uint64{1, 2, 3}}
	if err := w.SaveSnapshot(&walpb.Snapshot{Index: proto.Uint64(bootIndex), Term: proto.Uint64(bootTerm), ConfState: cs}); err != nil {
		t.Fatal(err)
	}
	if err := w.Save(hs(bootTerm, bootIndex), nil); err != nil {
		t.Fatal(err)
	}
	steps(w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// readerNode é um Node mínimo para o LogReader: MemoryStorage e commit publicado.
func readerNode(dir string, ms *raft.MemoryStorage, commit uint64) *Node {
	n := &Node{cfg: Config{Dir: dir}, storage: ms, commitCh: make(chan struct{})}
	n.commit.Store(commit)
	n.ret = newRetention(dir)
	n.reader = &LogReader{node: n, ret: n.ret, dir: dir}
	return n
}

// emptyRAM é um MemoryStorage vazio com o primeiro índice em first (tudo abaixo no disco).
func emptyRAM(first uint64) *raft.MemoryStorage {
	ms := raft.NewMemoryStorage()
	_ = ms.ApplySnapshot(&raftpb.Snapshot{Metadata: &raftpb.SnapshotMetadata{Index: proto.Uint64(first - 1), Term: proto.Uint64(3), ConfState: &raftpb.ConfState{Voters: []uint64{1}}}})
	return ms
}

// CA-20, SG-20: um WAL com entradas não commitadas da líder do termo 2 reescritas pela
// líder do termo 3. Saem só as versões commitadas, contíguas, e nada acima do último
// HardState.commit.
func TestLogReaderFinalEntryRule(t *testing.T) {
	const size = 1 << 10 // ~1 KiB por entrada: atravessa vários segmentos de 64 KiB
	dir := writeWAL(t, 1, "poc-local", func(w *wal.WAL) {
		// Uma entrada por Save, como o laço faz: o WAL corta segmentos no caminho.
		appendTerm := func(from, to, term uint64) {
			for i := from; i <= to; i++ {
				must(t, w.Save(&raftpb.HardState{}, []*raftpb.Entry{ent(i, term, size)}))
			}
		}
		appendTerm(2, 120, 1)
		must(t, w.Save(hs(1, 100), nil)) // 101..120 do termo 1 ainda não commitadas
		appendTerm(101, 140, 2)          // o termo 2 reescreve a partir de 101
		must(t, w.Save(hs(2, 110), nil))
		appendTerm(115, 200, 3)          // o termo 3 reescreve a partir de 115
		must(t, w.Save(hs(3, 180), nil)) // 181..200 nunca commitadas
	})
	segs, _ := newRetention(dir).Segments()
	if len(segs) < 3 {
		t.Fatalf("o teste devia atravessar vários segmentos, tem %d", len(segs))
	}
	n := readerNode(dir, emptyRAM(500), 200)
	var all []domain.Entry
	for next := domain.Index(2); next <= 200; {
		got, err := n.reader.Read(context.Background(), next, 200, 16<<10)
		if errors.Is(err, ErrNotRetained) {
			break // acabou o que está commitado no disco
		}
		if err != nil {
			t.Fatalf("Read(%d): %v", next, err)
		}
		all = append(all, got...)
		next = got[len(got)-1].Index + 1
	}
	if len(all) != 179 || all[0].Index != 2 || all[len(all)-1].Index != 180 {
		t.Fatalf("entregues %d entradas, de %d a %d; quer 2..180", len(all), all[0].Index, all[len(all)-1].Index)
	}
	for i, e := range all {
		if e.Index != domain.Index(i+2) {
			t.Fatalf("buraco: posição %d tem índice %d", i, e.Index)
		}
		want := domain.Term(1)
		switch {
		case e.Index >= 115:
			want = 3
		case e.Index >= 101:
			want = 2
		}
		if e.Term != want {
			t.Fatalf("índice %d com termo %d, quer %d (versão reescrita)", e.Index, e.Term, want)
		}
	}
	if n.ret.Pins() != 0 {
		t.Fatalf("pino ficou para trás: %d", n.ret.Pins())
	}
}

// CA-20: a leitura que atravessa da RAM para o disco devolve o mesmo que a só do disco.
func TestLogReaderCrossingEqualsDisk(t *testing.T) {
	var ents []*raftpb.Entry
	for i := uint64(2); i <= 300; i++ {
		ents = append(ents, ent(i, 1, 512))
	}
	dir := writeWAL(t, 1, "poc-local", func(w *wal.WAL) { must(t, w.Save(hs(1, 300), ents)) })
	ram := emptyRAM(150) // RAM com 150..300; abaixo, só no disco
	must(t, ram.Append(ents[148:]))
	readAll := func(n *Node) []domain.Entry {
		var out []domain.Entry
		for next := domain.Index(2); next <= 300; {
			got, err := n.reader.Read(context.Background(), next, 300, 8<<10)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, got...)
			next = got[len(got)-1].Index + 1
		}
		return out
	}
	crossing := readAll(readerNode(dir, ram, 300))
	disk := readAll(readerNode(dir, emptyRAM(1000), 300))
	if len(crossing) != 299 || len(disk) != 299 {
		t.Fatalf("atravessando %d, só disco %d; quer 299", len(crossing), len(disk))
	}
	for i := range disk {
		if crossing[i].Index != disk[i].Index || crossing[i].Term != disk[i].Term {
			t.Fatalf("posição %d: %+v != %+v", i, crossing[i], disk[i])
		}
	}
}

// RT-16: abaixo do primeiro segmento retido, ErrNotRetained; a cauda pré-alocada não é erro.
func TestLogReaderNotRetained(t *testing.T) {
	var ents []*raftpb.Entry
	for i := uint64(2); i <= 300; i++ {
		ents = append(ents, ent(i, 1, 1<<10))
	}
	dir := writeWAL(t, 1, "poc-local", func(w *wal.WAL) {
		for _, e := range ents { // um Save por entrada: o WAL corta segmentos no caminho
			must(t, w.Save(hs(1, e.GetIndex()), []*raftpb.Entry{e}))
		}
	})
	ret := newRetention(dir)
	segs, _ := ret.Segments()
	if len(segs) < 3 {
		t.Fatalf("segmentos = %d", len(segs))
	}
	must(t, os.Remove(filepath.Join(dir, segmentName(segs[0].Seq, uint64(segs[0].First)))))
	n := readerNode(dir, emptyRAM(1000), 300)
	if _, err := n.reader.Read(context.Background(), 2, 300, 1<<20); !errors.Is(err, ErrNotRetained) {
		t.Fatalf("abaixo do primeiro segmento: erro = %v, quer ErrNotRetained", err)
	}
	if fr := n.reader.FirstRetained(); fr != segs[1].First {
		t.Fatalf("FirstRetained = %d, quer %d", fr, segs[1].First)
	}
	got, err := n.reader.Read(context.Background(), segs[1].First, 300, 1<<30)
	if err != nil || got[len(got)-1].Index != 300 {
		t.Fatalf("leitura até a cauda pré-alocada: %v, %v", len(got), err)
	}
}

// CA-22 (unidade), RT-09: com 100.000 entradas e um snapshot recente, a partida traz para a
// RAM só a cauda depois do snapshot.
func TestStartupReadsOnlyTail(t *testing.T) {
	const total, snap = 100_001, 99_000
	dir := writeWAL(t, 1, "poc-local", func(w *wal.WAL) {
		batch := make([]*raftpb.Entry, 0, 5000)
		for i := uint64(2); i <= total; i++ {
			batch = append(batch, ent(i, 1, 16))
			if len(batch) == cap(batch) || i == total {
				must(t, w.Save(hs(1, i), batch))
				batch = batch[:0]
			}
		}
		must(t, w.SaveSnapshot(&walpb.Snapshot{Index: proto.Uint64(snap), Term: proto.Uint64(1), ConfState: &raftpb.ConfState{Voters: []uint64{1, 2, 3}}}))
	})
	m := &recMetrics{}
	n, err := Open(Config{ID: 1, Voters: []uint64{1, 2, 3}, Dir: dir, ClusterID: "poc-local", Metrics: m}, memSender{net: &memNet{}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	entries, _ := n.RAM()
	if entries != total-snap || n.SnapshotIndex() != snap {
		t.Fatalf("RAM com %d entradas e snapshot %d; quer %d e %d", entries, n.SnapshotIndex(), total-snap, snap)
	}
	if m.n("startup") != 1 {
		t.Fatal("a duração da partida não foi registrada")
	}
}

// RT-20: a identidade nova grava e relê; o formato antigo e outro cluster_id recusam.
func TestWALIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	cfg := Config{ID: 1, Voters: []uint64{1, 2, 3}, Dir: dir, ClusterID: "poc-local"}
	n, err := Open(cfg, memSender{net: &memNet{}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	must(t, n.Close())
	n, err = Open(cfg, memSender{net: &memNet{}}, zerolog.Nop())
	if err != nil {
		t.Fatalf("reabrir com a mesma identidade: %v", err)
	}
	must(t, n.Close())
	other := cfg
	other.ClusterID = "outro"
	if _, err := Open(other, memSender{net: &memNet{}}, zerolog.Nop()); !errors.Is(err, ErrIdentity) {
		t.Fatalf("outro cluster_id: erro = %v, quer ErrIdentity", err)
	}
	other = cfg
	other.ID = 2
	if _, err := Open(other, memSender{net: &memNet{}}, zerolog.Nop()); !errors.Is(err, ErrIdentity) {
		t.Fatalf("outro node_id: erro = %v, quer ErrIdentity", err)
	}

	oldDir := filepath.Join(t.TempDir(), "wal")
	w, err := wal.Create(zap.NewNop(), oldDir, []byte("1")) // formato antigo: só o número do nó
	if err != nil {
		t.Fatal(err)
	}
	must(t, w.SaveSnapshot(&walpb.Snapshot{Index: proto.Uint64(1), Term: proto.Uint64(1), ConfState: &raftpb.ConfState{Voters: []uint64{1, 2, 3}}}))
	must(t, w.Save(hs(1, 1), nil))
	must(t, w.Close())
	old := cfg
	old.Dir = oldDir
	if _, err := Open(old, memSender{net: &memNet{}}, zerolog.Nop()); !errors.Is(err, ErrIdentity) {
		t.Fatalf("formato antigo: erro = %v, quer ErrIdentity", err)
	}
}

// DC-06: o marcador vai e volta; uma queda entre o arquivo do marcador e o SaveSnapshot
// (marcador à frente do WAL) parte do snapshot do WAL com o confirmado do marcador.
func TestMarkerFileAndCrashBeforeSaveSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot")
	m := domain.SnapshotMarker{Version: 1, ClusterID: "poc-local", Epoch: 1, Confirmed: 77}
	must(t, writeMarker(path, m, 90))
	got, idx, err := readMarker(path, "poc-local")
	if err != nil || got != m || idx != 90 {
		t.Fatalf("readMarker = %+v %d %v", got, idx, err)
	}
	zero, idx, err := readMarker(filepath.Join(t.TempDir(), "ausente"), "poc-local")
	if err != nil || idx != 0 || zero.Version != 1 || zero.ClusterID != "poc-local" || zero.Epoch != 1 {
		t.Fatalf("marcador ausente = %+v %d %v", zero, idx, err)
	}
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 0xff
	must(t, os.WriteFile(path, b, 0o644))
	if _, _, err := readMarker(path, "poc-local"); !errors.Is(err, domain.ErrBadMarker) {
		t.Fatalf("marcador corrompido: erro = %v", err)
	}

	var ents []*raftpb.Entry
	for i := uint64(2); i <= 60; i++ {
		ents = append(ents, ent(i, 1, 16))
	}
	dir := writeWAL(t, 1, "poc-local", func(w *wal.WAL) {
		must(t, w.Save(hs(1, 60), ents))
		must(t, w.SaveSnapshot(&walpb.Snapshot{Index: proto.Uint64(40), Term: proto.Uint64(1), ConfState: &raftpb.ConfState{Voters: []uint64{1, 2, 3}}}))
	})
	mpath := filepath.Join(filepath.Dir(dir), "snapshot")
	must(t, writeMarker(mpath, m, 55)) // gravado para 55; o SaveSnapshot(55) não chegou
	n, err := Open(Config{ID: 1, Voters: []uint64{1, 2, 3}, Dir: dir, ClusterID: "poc-local", MarkerPath: mpath}, memSender{net: &memNet{}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if n.SnapshotIndex() != 40 || n.Marker().Confirmed != 77 {
		t.Fatalf("partida: snapshot %d confirmado %d; quer 40 e 77", n.SnapshotIndex(), n.Marker().Confirmed)
	}
	if s, _ := n.storage.Snapshot(); s.GetMetadata().GetIndex() != 40 {
		t.Fatalf("MemoryStorage começa em %d", s.GetMetadata().GetIndex())
	}
	if d, err := domain.DecodeMarker(mustSnap(t, n).GetData()); err != nil || d.Confirmed != 77 {
		t.Fatalf("Data do snapshot da partida = %+v, %v", d, err)
	}
}

func mustSnap(t *testing.T, n *Node) *raftpb.Snapshot {
	t.Helper()
	s, err := n.storage.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

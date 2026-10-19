package etcdraft

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// colProgress simula o progresso do applier: o durável é o que o coletor já leu. Em cada
// Sync confere a ordem do RT-11: um marcador do próprio Compactor (índice ≤ avançado)
// nunca está à frente do snapshot quando o progresso recebe o fsync (o fsync vem antes do
// arquivo do marcador). Um marcador recebido (RT-18) fica à frente do avançado até o
// Advance e não entra na conta.
type colProgress struct {
	t      *testing.T
	col    func() *collector
	node   func() *Node
	marker string
	syncs  atomic.Int64
}

func (p *colProgress) SaveProgress(domain.Index, uint32) error     { return nil }
func (p *colProgress) LoadProgress() (domain.Index, uint32, error) { return 0, 0, nil }
func (p *colProgress) Sync() (domain.Index, error) {
	p.syncs.Add(1)
	if n := p.node(); n != nil && p.marker != "" {
		if _, idx, err := readMarker(p.marker, ""); err == nil && idx > n.SnapshotIndex() && idx <= n.Advanced() {
			p.t.Errorf("SG-18/RT-11: marcador em %d à frente do snapshot %d no fsync do progresso", idx, n.SnapshotIndex())
		}
	}
	es := p.col().all()
	if len(es) == 0 {
		return 0, nil
	}
	return es[len(es)-1].Index, nil
}

// compacting liga a compactação no nó id do cluster.
func compacting(c *cluster, id uint64, every, catchup uint64, retainMax, ramMax int64) {
	p := &colProgress{t: c.t, col: func() *collector {
		c.net.mu.Lock()
		defer c.net.mu.Unlock()
		return c.got[id]
	}}
	p.node = func() *Node {
		c.net.mu.Lock()
		defer c.net.mu.Unlock()
		return c.net.nodes[id]
	}
	c.tune[id] = func(cfg *Config) {
		cfg.ClusterID = "poc-local"
		cfg.MarkerPath = filepath.Join(filepath.Dir(cfg.Dir), "snapshot")
		p.marker = cfg.MarkerPath
		cfg.Compaction = CompactionConfig{Progress: p, EveryEntries: every, EveryBytes: 1 << 40, CatchupEntries: catchup,
			RAMMax: ramMax, RetainMax: retainMax, Interval: 20 * time.Millisecond}
	}
}

// newCompactingCluster sobe 3 nós com a compactação ligada.
func newCompactingCluster(t *testing.T, ctx context.Context, every, catchup uint64, retainMax, ramMax int64) *cluster {
	base := t.TempDir()
	c := &cluster{
		t: t, net: &memNet{nodes: map[uint64]*Node{}, down: map[uint64]bool{}, ctx: ctx},
		dirs: map[uint64]string{}, cancel: map[uint64]context.CancelFunc{}, done: map[uint64]chan error{},
		got: map[uint64]*collector{}, m: map[uint64]*recMetrics{}, tune: map[uint64]func(*Config){},
		paused: map[uint64]bool{}, cdone: map[uint64]chan struct{}{},
	}
	for id := uint64(1); id <= 3; id++ {
		c.dirs[id] = filepath.Join(base, "n"+strconv.FormatUint(id, 10), "wal")
		compacting(c, id, every, catchup, retainMax, ramMax)
		c.start(ctx, id, []uint64{1, 2, 3})
	}
	return c
}

func proposeN(t *testing.T, ctx context.Context, l *Node, n int) {
	t.Helper()
	const clients = 32
	next := make(chan int, n)
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	var wg sync.WaitGroup
	for w := 0; w < clients; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				pctx, pc := context.WithTimeout(ctx, 10*time.Second)
				_, err := l.Propose(pctx, put("k"+strconv.Itoa(i)))
				pc()
				if err != nil {
					t.Errorf("Propose: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func stopAll(c *cluster) {
	for id := range c.dirs {
		if !c.net.down[id] {
			c.stop(id)
		}
	}
}

// CA-21 (unidade), RT-11, RT-14, SG-22: com o gatilho em 100 entradas, o snapshot avança
// nas 3 células e a RAM fica em ~100 + catchup, com 3.000 propostas concorrentes ao
// SaveSnapshot, sem troca de líder. O coletor (como o CommitPump) recebe tudo contíguo.
func TestCompactorBoundsRAMUnderLoad(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCompactingCluster(t, ctx, 100, 10, 1<<30, 1<<30)
	defer stopAll(c)
	l := c.leader(5 * time.Second)
	before := c.m[l.cfg.ID].n("leader")
	proposeN(t, ctx, l, 3000)
	if got := c.m[l.cfg.ID].n("leader"); got != before {
		t.Fatalf("troca de líder durante a compactação (%d → %d)", before, got)
	}
	for id := uint64(1); id <= 3; id++ {
		n := c.net.nodes[id]
		waitFor(t, 10*time.Second, func() bool {
			e, _ := n.RAM()
			return n.SnapshotIndex() >= 2900 && e <= 100+10+50
		}, "snapshot não avançou ou a RAM não foi compactada no nó "+strconv.FormatUint(id, 10))
		if c.m[id].n("snapshot") == 0 {
			t.Fatalf("nó %d: SnapshotTaken não registrado", id)
		}
		es := c.got[id].all()
		for i, e := range es {
			if e.Index != domain.Index(i+2) {
				t.Fatalf("nó %d: buraco na posição %d (%d)", id, i, e.Index)
			}
		}
	}
}

// DC-08: com uma follower parada DENTRO da janela, a RAM da líder fica segura pelo Match
// dela; com a janela pequena (a follower fora), a RAM é compactada; o teto da RAM
// compacta mesmo com a follower dentro da janela (SG-23).
func TestLeaderRAMHeldOnlyInsideWindow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retainMax int64
		ramMax    int64
		held      bool
	}{
		{"dentro da janela segura", 1 << 30, 1 << 30, true},
		{"fora da janela não segura", 1, 1 << 30, false},
		{"teto da RAM ignora o Match", 1 << 30, 16 << 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := newCompactingCluster(t, ctx, 100, 10, tc.retainMax, tc.ramMax)
			defer stopAll(c)
			l := c.leader(5 * time.Second)
			var down uint64
			for id := range c.dirs {
				if id != l.cfg.ID {
					down = id
					break
				}
			}
			c.stop(down)
			proposeN(t, ctx, l, 600)
			waitFor(t, 10*time.Second, func() bool { return l.SnapshotIndex() >= 500 }, "a líder não fez snapshot")
			time.Sleep(200 * time.Millisecond) // alguns ciclos do Compactor
			entries, _ := l.RAM()
			if tc.held && entries < 500 {
				t.Fatalf("RAM da líder com %d entradas: a follower na janela devia segurar", entries)
			}
			if !tc.held && entries > 300 {
				t.Fatalf("RAM da líder com %d entradas: devia ter compactado", entries)
			}
			if st := l.Status(); st.Window == nil || st.Window[down] != tc.held && tc.ramMax > 1<<20 {
				t.Fatalf("janela no /status = %v", st.Window)
			}
		})
	}
}

// fakePolicy é a política do MsgSnap com o aplicado controlado pelo teste.
type fakePolicy struct {
	applied atomic.Uint64
	behind  atomic.Uint64
}

func (p *fakePolicy) Applied() domain.Index      { return domain.Index(p.applied.Load()) }
func (p *fakePolicy) Behind(target domain.Index) { p.behind.Store(uint64(target)) }

// CA-24 (unidade), RT-17, RT-18, SG-19: uma follower desligada por 500 entradas, com a
// líder reiniciada (a cauda força o MsgSnap). Com o aplicado < S, nada chega ao Raft da
// follower e Behind(S) é chamado; com o aplicado ≥ S, ela aceita e grava o snapshot, e o
// WAL relido depois de um reinício parte de S.
func TestMsgSnapDeferredThenAccepted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCompactingCluster(t, ctx, 100, 10, 1<<30, 1<<30)
	defer stopAll(c)
	l := c.leader(5 * time.Second)
	var f uint64
	for id := range c.dirs {
		if id != l.cfg.ID {
			f = id
			break
		}
	}
	c.stop(f)
	proposeN(t, ctx, l, 500)
	waitFor(t, 10*time.Second, func() bool { return l.SnapshotIndex() >= 400 }, "a líder não fez snapshot")
	c.stop(l.cfg.ID) // reinicia a líder: a RAM dela volta só com a cauda (DC-08)
	c.start(ctx, l.cfg.ID, []uint64{1, 2, 3})
	l = c.leader(5 * time.Second)
	waitFor(t, 10*time.Second, func() bool { return l.SnapshotIndex() >= 400 }, "a nova líder sem snapshot")

	pol := &fakePolicy{}
	prevTune := c.tune[f]
	c.tune[f] = func(cfg *Config) { prevTune(cfg); cfg.Policy = pol }
	c.paused[f] = true // o coletor da follower não tem como ler o que veio no marcador
	c.start(ctx, f, []uint64{1, 2, 3})
	fn := c.net.nodes[f]
	waitFor(t, 10*time.Second, func() bool { return pol.behind.Load() >= 400 }, "Behind(S) não foi chamado")
	if fn.CommitIndex() >= domain.Index(pol.behind.Load()) || fn.SnapshotIndex() != bootIndex {
		t.Fatalf("SG-19: o marcador chegou ao Raft (commit %d, snapshot %d)", fn.CommitIndex(), fn.SnapshotIndex())
	}
	if !fn.Status().BehindTail || c.m[f].n("snapshot_deferred") == 0 {
		t.Fatal("o /status devia mostrar a célula atrás da cauda e a métrica do adiamento")
	}

	pol.applied.Store(1 << 40) // o banco já tem tudo (como depois da recuperação, Incremento B)
	waitFor(t, 10*time.Second, func() bool { return fn.SnapshotIndex() >= 400 }, "a follower não aceitou o marcador")
	s := fn.SnapshotIndex()
	if fn.Status().BehindTail {
		t.Fatal("com o aplicado à frente, a célula não está mais atrás da cauda")
	}
	c.stop(f)
	n, err := Open(Config{ID: f, Voters: []uint64{1, 2, 3}, Dir: c.dirs[f], ClusterID: "poc-local",
		MarkerPath: filepath.Join(filepath.Dir(c.dirs[f]), "snapshot")}, memSender{net: &memNet{}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if n.SnapshotIndex() < s {
		t.Fatalf("o WAL relido parte de %d, quer >= %d", n.SnapshotIndex(), s)
	}
}

// RT-18: um marcador de outro grupo, ilegível ou à frente do aplicado não chega ao Raft.
func TestAcceptSnapshotRules(t *testing.T) {
	pol := &fakePolicy{}
	m := &recMetrics{}
	n := &Node{cfg: Config{ClusterID: "poc-local", Policy: pol}, log: zerolog.Nop(), metrics: m, snapLog: newRateLog(time.Second)}
	msg := func(cluster string, idx uint64) *raftpb.Message {
		d := domain.SnapshotMarker{Version: 1, ClusterID: cluster, Epoch: 1}.Encode()
		return &raftpb.Message{Type: raftpb.MsgSnap.Enum(), Snapshot: &raftpb.Snapshot{Data: d,
			Metadata: &raftpb.SnapshotMetadata{Index: proto.Uint64(idx), Term: proto.Uint64(1)}}}
	}
	if n.acceptSnapshot(msg("outro", 10)) {
		t.Fatal("marcador de outro grupo aceito")
	}
	bad := msg("poc-local", 10)
	bad.Snapshot.Data = []byte("lixo")
	if n.acceptSnapshot(bad) {
		t.Fatal("marcador ilegível aceito")
	}
	if n.acceptSnapshot(msg("poc-local", 10)) || pol.behind.Load() != 10 || m.n("snapshot_deferred") != 1 {
		t.Fatal("aplicado atrás: devia descartar e chamar Behind(10)")
	}
	pol.applied.Store(10)
	if !n.acceptSnapshot(msg("poc-local", 10)) {
		t.Fatal("aplicado = S: devia aceitar")
	}
}

// RT-14: a contagem de bytes da RAM acompanha Append (com truncamento pela troca de
// líder), Compact e ApplySnapshot.
func TestRAMTracker(t *testing.T) {
	var r ramTracker
	r.recordSnapshot(1)
	mk := func(from, to, size uint64) []*raftpb.Entry {
		var out []*raftpb.Entry
		for i := from; i <= to; i++ {
			out = append(out, ent(i, 1, int(size)))
		}
		return out
	}
	sz := int64(proto.Size(ent(2, 1, 100)))
	r.recordAppend(mk(2, 11, 100))
	if e, b := r.stats(); e != 10 || b != 10*sz {
		t.Fatalf("depois do Append: %d entradas, %d bytes", e, b)
	}
	r.recordAppend(mk(8, 9, 100)) // a nova líder reescreve a partir de 8
	if e, b := r.stats(); e != 8 || b != 8*sz {
		t.Fatalf("depois do truncamento: %d entradas, %d bytes", e, b)
	}
	r.recordCompact(4)
	if e, b := r.stats(); e != 5 || b != 5*sz {
		t.Fatalf("depois do Compact(4): %d entradas, %d bytes", e, b)
	}
	r.recordAppend(mk(3, 3, 100)) // já compactado: ignorado
	if e, _ := r.stats(); e != 5 {
		t.Fatalf("entrada compactada contada: %d", e)
	}
	r.recordSnapshot(50)
	if e, b := r.stats(); e != 0 || b != 0 {
		t.Fatalf("depois do snapshot: %d entradas, %d bytes", e, b)
	}
	if r.appended.Load() != 12*sz {
		t.Fatalf("acrescentados = %d, quer %d", r.appended.Load(), 12*sz)
	}
}

// fixedProgress devolve um aplicado durável fixo.
type fixedProgress struct{ durable atomic.Uint64 }

func (p *fixedProgress) SaveProgress(domain.Index, uint32) error     { return nil }
func (p *fixedProgress) LoadProgress() (domain.Index, uint32, error) { return 0, 0, nil }
func (p *fixedProgress) Sync() (domain.Index, error)                 { return domain.Index(p.durable.Load()), nil }

// RT-15, SG-18: o disco nunca é liberado além do aplicado durável; um pino segura; o teto
// duro solta só os pinos de reconstrução e marca quem segura pelo aplicado.
func TestReleaseDiskRules(t *testing.T) {
	var ents []*raftpb.Entry
	for i := uint64(2); i <= 400; i++ {
		ents = append(ents, ent(i, 1, 1<<10))
	}
	dir := writeWAL(t, 1, "poc-local", func(w *wal.WAL) {
		for _, e := range ents {
			must(t, w.Save(hs(1, e.GetIndex()), []*raftpb.Entry{e}))
		}
	})
	prog := &fixedProgress{}
	n, err := Open(Config{ID: 1, Voters: []uint64{1, 2, 3}, Dir: dir, ClusterID: "poc-local",
		Compaction: CompactionConfig{Progress: prog, RetainMax: 1 << 30}}, memSender{net: &memNet{}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.setSnapshot(380, domain.SnapshotMarker{Version: 1, ClusterID: "poc-local", Epoch: 1})
	n.ret.SetFloor(1000) // follower: piso alto recebido da líder
	c := n.compactor
	c.usage = func(string) (float64, error) { return 0, nil }
	segs := func() []domain.Segment { s, _ := n.ret.Segments(); return s }
	first := func() domain.Index { return segs()[0].First }
	all := segs()
	if len(all) < 5 {
		t.Fatalf("segmentos = %d", len(all))
	}

	prog.durable.Store(uint64(all[2].First) - 1) // o aplicado durável segura
	must(t, c.releaseDisk(time.Now(), segs(), n.ret.Floor(), nil))
	if first() > domain.Index(prog.durable.Load())+1 {
		t.Fatalf("SG-18: liberou além do aplicado durável (%d): primeiro retido %d", prog.durable.Load(), first())
	}
	removedUpTo := first()

	prog.durable.Store(380)
	release := n.ret.Pin("ler-x", removedUpTo) // um pino segura a partir dele
	must(t, c.releaseDisk(time.Now(), segs(), n.ret.Floor(), nil))
	if first() > removedUpTo {
		t.Fatalf("o pino em %d não segurou: primeiro retido %d", removedUpTo, first())
	}
	release()

	n.ret.PinFor(rebuildPinPrefix+"2", removedUpTo, 0)
	keep := n.ret.Pin("ler-y", removedUpTo+1)
	c.cfg.HardBytes = 1 // teto duro sempre estourado
	must(t, c.releaseDisk(time.Now(), segs(), n.ret.Floor(), nil))
	if n.ret.hasRebuildPins() {
		t.Fatal("o teto duro devia soltar o pino de reconstrução")
	}
	if n.ret.Pins() != 1 {
		t.Fatalf("o teto duro solta só os pinos de reconstrução; restam %d", n.ret.Pins())
	}
	if st := c.Status(); !st.HardCeiling || st.HeldByApplied {
		t.Fatalf("status no teto = %+v", st)
	}
	keep()

	prog.durable.Store(uint64(first())) // agora quem segura é o aplicado local
	must(t, c.releaseDisk(time.Now(), segs(), n.ret.Floor(), nil))
	if st := c.Status(); !st.HeldByApplied {
		t.Fatalf("o aplicado local segura no teto: status = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, segmentName(all[len(all)-1].Seq, uint64(all[len(all)-1].First)))); err != nil {
		t.Fatalf("a cauda nunca sai: %v", err)
	}
}

// DC-07: o tamanho do segmento é ajustado uma vez por processo.
func TestSegmentSizeOnce(t *testing.T) {
	if err := setSegmentSize(testSegment); err != nil {
		t.Fatal(err)
	}
	if err := setSegmentSize(0); err != nil {
		t.Fatal("0 não altera e não é erro")
	}
	if err := setSegmentSize(testSegment * 2); err == nil {
		t.Fatal("um segundo valor diferente devia ser erro")
	}
}

var (
	_ port.ApplyProgress  = (*colProgress)(nil)
	_ port.SnapshotPolicy = (*fakePolicy)(nil)
	_                     = errors.New
)

// RT-09, RT-15: depois de o Compactor apagar segmentos do disco, a célula reinicia a partir
// do último snapshot, e um leitor que começa no primeiro retido (como o CommitPump na
// partida) recebe tudo contíguo até o commit, atravessando o disco e a RAM.
func TestRestartAfterDiskRelease(t *testing.T) {
	must(t, setSegmentSize(testSegment))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCompactingCluster(t, ctx, 100, 10, 1<<30, 1<<30)
	defer stopAll(c)
	l := c.leader(5 * time.Second)
	big := strings.Repeat("x", 1<<10)
	for i := 0; i < 600; i++ {
		pctx, pc := context.WithTimeout(ctx, 5*time.Second)
		e := domain.Entry{Kind: domain.KindWrite, Ops: []domain.Op{{Table: "t_{cell}", Kind: domain.OpPut,
			Key: []byte(strconv.Itoa(i)), Item: []byte(`{"v":{"S":"` + big + `"}}`)}}}
		_, err := l.Propose(pctx, e)
		pc()
		must(t, err)
	}
	id := l.cfg.ID
	segsOf := func() int { s, _ := newRetention(c.dirs[id]).Segments(); return len(s) }
	waitFor(t, 10*time.Second, func() bool { return segsOf() <= 3 && c.net.nodes[id].SnapshotIndex() >= 500 },
		"o Compactor não liberou o disco")
	first := c.net.nodes[id].FirstIndex()
	if first <= bootIndex+1 {
		t.Fatalf("nada saiu do disco: primeiro retido %d", first)
	}
	c.stop(id)
	c.start(ctx, id, []uint64{1, 2, 3})
	n := c.net.nodes[id]
	waitFor(t, 10*time.Second, func() bool { return n.CommitIndex() >= 601 }, "a célula reiniciada não alcançou o commit")
	var got []domain.Entry
	for next := n.FirstIndex(); next <= n.CommitIndex(); {
		es, err := n.CommittedEntries(next, n.CommitIndex(), 64<<10)
		if err != nil {
			t.Fatalf("CommittedEntries(%d): %v", next, err)
		}
		got = append(got, es...)
		next = es[len(es)-1].Index + 1
	}
	for i := 1; i < len(got); i++ {
		if got[i].Index != got[i-1].Index+1 {
			t.Fatalf("buraco depois de %d", got[i-1].Index)
		}
	}
	if got[0].Index != first || got[len(got)-1].Index < 601 {
		t.Fatalf("leitura de %d a %d; quer de %d até pelo menos 601", got[0].Index, got[len(got)-1].Index, first)
	}
}

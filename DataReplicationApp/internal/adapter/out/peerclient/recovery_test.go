package peerclient

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"example.com/datareplication/internal/adapter/in/peergrpc"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// fakeLog é um log com as entradas de first a commit.
type fakeLog struct {
	mu            sync.Mutex
	first, commit domain.Index
}

func (f *fakeLog) Read(_ context.Context, lo, hi domain.Index, _ uint64) ([]domain.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if lo < f.first {
		return nil, port.ErrNotRetained
	}
	hi = min(hi, f.commit)
	var out []domain.Entry
	for i := lo; i <= hi && len(out) < 3; i++ { // lotes pequenos: o fluxo manda várias mensagens
		out = append(out, domain.Entry{Index: i, Term: 1})
	}
	return out, nil
}

func (f *fakeLog) FirstRetained() domain.Index {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.first
}

func (f *fakeLog) setCommit(i domain.Index) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commit = i
}

// fakePins guarda os pinos com o índice e a validade.
type fakePins struct {
	mu   sync.Mutex
	pins map[string]domain.Index
	ttl  map[string]time.Duration
	seen map[string]bool // donos que já tiveram pino
}

func newFakePins() *fakePins {
	return &fakePins{pins: map[string]domain.Index{}, ttl: map[string]time.Duration{}, seen: map[string]bool{}}
}

func (p *fakePins) Pin(owner string, idx domain.Index) func() {
	p.PinFor(owner, idx, 0)
	return func() { p.Unpin(owner) }
}

func (p *fakePins) PinFor(owner string, idx domain.Index, ttl time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pins[owner], p.ttl[owner], p.seen[owner] = idx, ttl, true
}

func (p *fakePins) Unpin(owner string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.pins, owner)
}

func (p *fakePins) Has(owner string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.pins[owner]
	return ok
}

func (p *fakePins) get(owner string) (domain.Index, time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, ok := p.pins[owner]
	return i, p.ttl[owner], ok
}

func (p *fakePins) wasPinned(owner string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[owner]
}

// fakeScanner devolve duas páginas por tabela; onPage roda antes de cada página.
type fakeScanner struct {
	port.Scanner
	onPage func()
}

func (f fakeScanner) ScanVersioned(_ context.Context, t domain.LogicalTable, _ int, start []byte) (port.VersionedPage, error) {
	if f.onPage != nil {
		f.onPage()
	}
	if start == nil {
		return port.VersionedPage{Items: []port.VersionedItem{{Key: []byte(t + "-1"), Item: []byte(`{}`), Version: 3}},
			LastKey: []byte("k1")}, nil
	}
	return port.VersionedPage{Items: []port.VersionedItem{{Key: []byte(t + "-2"), Item: []byte(`{}`), Version: 4}}}, nil
}

type fakeCatalog struct{}

func (fakeCatalog) KeySchema(domain.LogicalTable) (domain.KeySchema, bool) {
	return domain.KeySchema{Partition: domain.KeyAttr{Name: "pk", Type: "S"}}, true
}
func (fakeCatalog) Ready() bool { return true }

type fakeStatus struct{ info port.RaftInfo }

func (f fakeStatus) Status() port.RaftInfo { return f.info }

type fakeApplied struct{ idx domain.Index }

func (f fakeApplied) WaitApplied(context.Context, domain.Index) error { return nil }
func (f fakeApplied) Applied() domain.Index                           { return f.idx }

type recoveryFixture struct {
	log  *fakeLog
	pins *fakePins
	gate *domain.Gate
	mode *domain.CellMode
	srv  *peergrpc.Server
}

func newRecoveryFixture(scan fakeScanner) *recoveryFixture {
	f := &recoveryFixture{log: &fakeLog{first: 5, commit: 20}, pins: newFakePins(), gate: &domain.Gate{}, mode: &domain.CellMode{}}
	f.gate.SetRaftCommit(20)
	f.srv = &peergrpc.Server{Raft: &fakeRaft{}, Writer: &fakeWriter{}, Barrier: fakeBarrier{}, Gate: &fakeGate{},
		Log: f.log, Pins: f.pins, Scanner: scan, Catalog: fakeCatalog{}, Mode: f.mode, SourceGate: f.gate,
		Applied: fakeApplied{idx: 17}, Status: fakeStatus{port.RaftInfo{NodeID: 2, Lead: 3, Commit: 20}},
		Tables: []domain.LogicalTable{"a", "b"}, PinTTL: time.Minute,
		Identity: func() (string, uint64) { return "poc", 1 },
		Genesis:  func() map[uint64]string { return map[uint64]string{1: "t1"} }}
	return f
}

func drainEntries(t *testing.T, s port.EntryStream) []domain.Entry {
	t.Helper()
	var out []domain.Entry
	for {
		b, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if len(b.Entries) > 0 && domain.Index(b.SourceGate) < b.Entries[len(b.Entries)-1].Index {
			t.Fatalf("SG-21: lote até %d além do gate da fonte %d", b.Entries[len(b.Entries)-1].Index, b.SourceGate)
		}
		out = append(out, b.Entries...)
	}
}

// RT-19: o Retained devolve o retido, o estado, a identidade e o Genesis.
func TestRetainedRoundTrip(t *testing.T) {
	f := newRecoveryFixture(fakeScanner{})
	c := setup(t, f.srv)
	info, err := c.Retained(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if info.Node != 2 || info.FirstRetained != 5 || info.Commit != 20 || info.Gate != 20 || info.State != "normal" ||
		info.Leader || info.ClusterID != "poc" || info.Epoch != 1 || info.Genesis[1] != "t1" || info.EmptyVolume {
		t.Fatalf("Retained = %+v", info)
	}
	f.srv.PreBoot = &peergrpc.PreBoot{ClusterID: "poc", BootToken: "tok"}
	info, err = c.Retained(context.Background(), 2)
	if err != nil || !info.EmptyVolume || info.BootToken != "tok" || info.State != "partida" {
		t.Fatalf("pré-partida (DC-12) = %+v %v", info, err)
	}
}

// SG-21: com o To acima do gate da fonte, o CatchUp espera o gate e nunca envia além dele.
func TestCatchUpWaitsForSourceGate(t *testing.T) {
	f := newRecoveryFixture(fakeScanner{})
	f.log.setCommit(30)
	c := setup(t, f.srv)
	s, err := c.CatchUp(context.Background(), 2, 6, 25)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.gate.SetRaftCommit(25)
	}()
	got := drainEntries(t, s)
	if len(got) != 20 || got[0].Index != 6 || got[19].Index != 25 {
		t.Fatalf("entradas: %d (de %v)", len(got), got)
	}
	for i, e := range got {
		if e.Index != domain.Index(6+i) {
			t.Fatalf("buraco em %d", 6+i)
		}
	}
}

// RT-19: abaixo do primeiro retido, NOT_RETAINED; fora de normal, SOURCE_NOT_READY.
func TestCatchUpCodes(t *testing.T) {
	f := newRecoveryFixture(fakeScanner{})
	c := setup(t, f.srv)
	s, err := c.CatchUp(context.Background(), 2, 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recv(); !errors.Is(err, port.ErrNotRetained) {
		t.Fatalf("abaixo do retido = %v", err)
	}
	s.Close()
	f.mode.EnterRecovering(50)
	s, _ = c.CatchUp(context.Background(), 2, 6, 10)
	if _, err := s.Recv(); !errors.Is(err, port.ErrSourceNotReady) {
		t.Fatalf("fonte fora de normal = %v", err)
	}
	s.Close()
}

// RT-15, DC-10: o pino do CatchUp existe durante o fluxo, some no fim, e o começo do
// CatchUp solta o pino de reconstrução do mesmo dono.
func TestCatchUpPinLifecycle(t *testing.T) {
	f := newRecoveryFixture(fakeScanner{})
	f.pins.PinFor("reconstrucao-1", 10, time.Minute)
	c := setup(t, f.srv)
	s, err := c.CatchUp(context.Background(), 2, 6, 30) // além do gate: o fluxo fica aberto
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.pins.get("catchup-1"); !ok {
		t.Fatal("pino do CatchUp devia existir durante o fluxo")
	}
	if f.pins.Has("reconstrucao-1") {
		t.Fatal("DC-10: o CatchUp da célula reconstruída devia soltar o pino da reconstrução")
	}
	s.Close()
	deadline := time.Now().Add(2 * time.Second)
	for f.pins.Has("catchup-1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.pins.Has("catchup-1") {
		t.Fatal("pino do CatchUp devia sumir no fim do fluxo")
	}
}

// AP-18: o CopyTable copia as duas tabelas, com o F na primeira página de cada uma, e
// deixa o pino com TTL no fim (DC-10).
func TestCopyTableTwoTables(t *testing.T) {
	f := newRecoveryFixture(fakeScanner{})
	c := setup(t, f.srv)
	s, err := c.CopyTable(context.Background(), 2, []domain.LogicalTable{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var pages []port.CopyPage
	for {
		p, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, p)
	}
	if len(pages) != 5 || !pages[4].Done {
		t.Fatalf("páginas: %+v", pages)
	}
	if pages[0].F != 17 || pages[0].KeySchema.Partition.Name != "pk" || pages[0].Table != "a" || pages[1].F != 0 ||
		!pages[1].TableDone || pages[2].F != 17 || pages[2].Table != "b" || !pages[3].TableDone {
		t.Fatalf("F e KeySchema na primeira página de cada tabela: %+v", pages)
	}
	if pages[0].Items[0].Version != 3 {
		t.Fatalf("versão do item: %+v", pages[0].Items)
	}
	idx, ttl, ok := f.pins.get("reconstrucao-1")
	if !ok || idx != 16 || ttl != time.Minute {
		t.Fatalf("DC-10: pino depois do fluxo = %d %v %v, quer F−1 = 16 com o REBUILD_PIN_TTL", idx, ttl, ok)
	}
}

// RT-15: o teto duro solta o pino no meio da cópia e o fluxo termina com REBUILD_ABORTED.
func TestCopyTableAbortedWhenPinDropped(t *testing.T) {
	var f *recoveryFixture
	pages := 0
	f = newRecoveryFixture(fakeScanner{onPage: func() {
		if pages++; pages == 1 {
			f.pins.Unpin("reconstrucao-1") // o Compactor chamou DropRebuildPins
		}
	}})
	c := setup(t, f.srv)
	s, err := c.CopyTable(context.Background(), 2, []domain.LogicalTable{"a"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for {
		_, err := s.Recv()
		if errors.Is(err, usecase.ErrRebuildAborted) {
			break
		}
		if err != nil {
			t.Fatalf("esperava REBUILD_ABORTED, veio %v", err)
		}
	}
	if !f.pins.wasPinned("reconstrucao-1") || f.pins.Has("reconstrucao-1") {
		t.Fatal("o pino abortado não volta com TTL")
	}
}

// DC-19: a conexão de um par que cai noutra célula é notada: o Retained com outro nó vira
// ErrSourceGone, e o CatchUp para outro destino é recusado.
func TestWrongDestinationRefused(t *testing.T) {
	f := newRecoveryFixture(fakeScanner{})
	f.srv.Self = 3 // o par 2 do cliente caiu no nó 3
	f.srv.Status = fakeStatus{port.RaftInfo{NodeID: 3, Lead: 3, Commit: 20}}
	c := setup(t, f.srv)
	if _, err := c.Retained(context.Background(), 2); !errors.Is(err, port.ErrSourceGone) {
		t.Fatalf("Retained de outro nó = %v", err)
	}
	s, err := c.CatchUp(context.Background(), 2, 6, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Recv(); !errors.Is(err, port.ErrSourceNotReady) {
		t.Fatalf("CatchUp para outro destino = %v", err)
	}
}

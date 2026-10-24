package usecase

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// copyClient serve o Retained e um CopyTable com as páginas dadas; abortAfter > 0 faz a
// primeira cópia terminar com REBUILD_ABORTED depois desse número de páginas.
type copyClient struct {
	mu         sync.Mutex
	info       port.RetainedInfo
	pages      []port.CopyPage
	abortAfter int
	copies     int
}

func (c *copyClient) Retained(context.Context, uint64) (port.RetainedInfo, error) { return c.info, nil }
func (c *copyClient) CatchUp(context.Context, uint64, domain.Index, domain.Index) (port.EntryStream, error) {
	return nil, errors.New("não usado")
}
func (c *copyClient) CopyTable(context.Context, uint64, []domain.LogicalTable) (port.CopyStream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.copies++
	abort := 0
	if c.copies == 1 {
		abort = c.abortAfter
	}
	return &fakeCopyStream{pages: append([]port.CopyPage(nil), c.pages...), abort: abort}, nil
}

func (c *copyClient) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.copies
}

type fakeCopyStream struct {
	pages []port.CopyPage
	sent  int
	abort int
}

func (s *fakeCopyStream) Recv() (port.CopyPage, error) {
	if s.abort > 0 && s.sent == s.abort {
		return port.CopyPage{}, ErrRebuildAborted
	}
	if len(s.pages) == 0 {
		return port.CopyPage{}, io.EOF
	}
	p := s.pages[0]
	s.pages, s.sent = s.pages[1:], s.sent+1
	return p, nil
}
func (s *fakeCopyStream) Close() {}

// versionStore é um banco com a condição de versão do AP-04.
type versionStore struct {
	mu    sync.Mutex
	items map[string]domain.Index
	puts  int
}

func (s *versionStore) ApplyBatch(_ context.Context, ops []port.VersionedOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range ops {
		s.puts++
		k := string(op.Table) + "/" + string(op.Key)
		if cur, ok := s.items[k]; !ok || cur < op.Version {
			s.items[k] = op.Version
		}
	}
	return nil
}
func (s *versionStore) Get(context.Context, domain.LogicalTable, []byte) ([]byte, error) {
	return nil, nil
}

func (s *versionStore) get(k string) (domain.Index, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[k]
	return v, ok
}

type schemaCatalog map[domain.LogicalTable]domain.KeySchema

func (c schemaCatalog) KeySchema(t domain.LogicalTable) (domain.KeySchema, bool) {
	ks, ok := c[t]
	return ks, ok
}
func (schemaCatalog) Ready() bool { return true }

// memProgress guarda o último progresso e conta os Sync.
type memProgress struct {
	mu    sync.Mutex
	idx   domain.Index
	ord   uint32
	syncs int
}

func (p *memProgress) SaveProgress(i domain.Index, o uint32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idx, p.ord = i, o
	return nil
}
func (p *memProgress) LoadProgress() (domain.Index, uint32, error) { return p.idx, p.ord, nil }
func (p *memProgress) Sync() (domain.Index, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.syncs++
	return p.idx, nil
}

var pkSchema = domain.KeySchema{Partition: domain.KeyAttr{Name: "pk", Type: "S"}}

func twoTablePages() []port.CopyPage {
	return []port.CopyPage{
		{F: 40, Table: "a", KeySchema: pkSchema, Items: []port.VersionedItem{{Key: []byte("1"), Version: 10}, {Key: []byte("2"), Version: 3}}},
		{Table: "a", Items: []port.VersionedItem{{Key: []byte("3"), Version: 41}}, TableDone: true},
		{F: 40, Table: "b", KeySchema: pkSchema, Items: []port.VersionedItem{{Key: []byte("9"), Version: 7}}, TableDone: true},
		{F: 40, Done: true},
	}
}

type rebuildRig struct {
	client *copyClient
	store  *versionStore
	prog   *memProgress
	mode   *domain.CellMode
	rb     *Rebuild
	idle   *bool
	imu    *sync.Mutex
}

func newRebuildRig(cat schemaCatalog) *rebuildRig {
	idle, imu := true, &sync.Mutex{}
	r := &rebuildRig{client: &copyClient{info: port.RetainedInfo{State: "normal", ClusterID: "poc", Epoch: 1, Gate: 50},
		pages: twoTablePages()}, store: &versionStore{items: map[string]domain.Index{}}, prog: &memProgress{},
		mode: &domain.CellMode{}, idle: &idle, imu: imu}
	r.rb = &Rebuild{Self: 1, Peers: []uint64{1, 2}, Client: r.client, Catalog: cat,
		Identity: func() (string, uint64) { return "poc", 1 }, Store: r.store, Progress: r.prog, Mode: r.mode,
		Idle: func() bool { imu.Lock(); defer imu.Unlock(); return *r.idle }, IsLeader: func() bool { return false },
		Tables: []domain.LogicalTable{"a", "b"}, Retry: 10 * time.Millisecond, Log: zerolog.Nop()}
	return r
}

func (r *rebuildRig) setIdle(v bool) {
	r.imu.Lock()
	defer r.imu.Unlock()
	*r.idle = v
}

func (r *rebuildRig) run(t *testing.T) chan error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.rb.Run(ctx) }()
	t.Cleanup(cancel)
	return done
}

func waitErr(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("a reconstrução não terminou")
		return nil
	}
}

var bothTables = schemaCatalog{"a": pkSchema, "b": pkSchema}

// AP-18, DC-04: copia as duas tabelas com a versão original, grava (F, OrdComplete) com
// fsync e pede o reinício.
func TestRebuildCopiesTwoTables(t *testing.T) {
	r := newRebuildRig(bothTables)
	done := r.run(t)
	r.mode.EnterRebuilding()
	if err := waitErr(t, done); !errors.Is(err, ErrRestartAfterRebuild) {
		t.Fatalf("Run = %v", err)
	}
	for k, want := range map[string]domain.Index{"a/1": 10, "a/2": 3, "a/3": 41, "b/9": 7} {
		if v, _ := r.store.get(k); v != want {
			t.Fatalf("%s = %d, quer %d (versão original)", k, v, want)
		}
	}
	if r.prog.idx != 40 || r.prog.ord != port.OrdComplete || r.prog.syncs == 0 {
		t.Fatalf("progresso (%d, %d) syncs %d", r.prog.idx, r.prog.ord, r.prog.syncs)
	}
	if st := r.rb.Status(); st.F != 40 || st.ItensCopiados != 4 {
		t.Fatalf("status %+v", st)
	}
}

// DC-11: a cópia sobrescreve no lugar: um item mais novo não regride (SG-06) e um item que
// só existe na célula continua lá (o limite do delete, AP-19).
func TestRebuildOverwritesInPlace(t *testing.T) {
	r := newRebuildRig(bothTables)
	r.store.items["a/2"] = 30      // mais novo que o copiado (3)
	r.store.items["a/so-aqui"] = 5 // sem par na fonte
	done := r.run(t)
	r.mode.EnterRebuilding()
	if err := waitErr(t, done); !errors.Is(err, ErrRestartAfterRebuild) {
		t.Fatal(err)
	}
	if v, _ := r.store.get("a/2"); v != 30 {
		t.Fatalf("o item mais novo regrediu para %d", v)
	}
	if _, ok := r.store.get("a/so-aqui"); !ok {
		t.Fatal("DC-11: nada é apagado")
	}
}

// RT-15: abortada no teto duro, espera REBUILD_RETRY e copia de novo sobre a tabela que
// ficou, sem apagá-la.
func TestRebuildRetriesAfterAbort(t *testing.T) {
	r := newRebuildRig(bothTables)
	r.client.abortAfter = 1
	done := r.run(t)
	r.mode.EnterRebuilding()
	if err := waitErr(t, done); !errors.Is(err, ErrRestartAfterRebuild) {
		t.Fatal(err)
	}
	if r.client.n() != 2 {
		t.Fatalf("cópias = %d, quer 2", r.client.n())
	}
	if v, _ := r.store.get("a/1"); v != 10 {
		t.Fatalf("a/1 = %d", v)
	}
}

// DC-11: outra história ou KeySchema diferente → recusa, nenhuma gravação e nenhuma nova
// tentativa automática; um novo Trigger tenta de novo.
func TestRebuildRefuses(t *testing.T) {
	for name, setup := range map[string]func(*rebuildRig){
		"cluster": func(r *rebuildRig) { r.client.info.ClusterID = "outro" },
		"epoch":   func(r *rebuildRig) { r.client.info.Epoch = 2 },
		"schema": func(r *rebuildRig) {
			r.rb.Catalog = schemaCatalog{"a": {Partition: domain.KeyAttr{Name: "id", Type: "S"}}, "b": pkSchema}
		},
		"ausente": func(r *rebuildRig) { r.rb.Catalog = schemaCatalog{"b": pkSchema} },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRebuildRig(bothTables)
			setup(r)
			r.run(t)
			r.mode.EnterRebuilding()
			waitFor(t, "recusa", r.rb.Refused)
			time.Sleep(50 * time.Millisecond) // várias vezes o Retry: não tenta de novo sozinha
			r.store.mu.Lock()
			puts := r.store.puts
			r.store.mu.Unlock()
			if puts != 0 {
				t.Fatalf("gravou %d itens", puts)
			}
			copies := r.client.n()
			if (name == "cluster" || name == "epoch") && copies != 0 {
				t.Fatalf("com outra história, nem abre a cópia: %d", copies)
			}
			if r.client.n() != copies {
				t.Fatal("tentou de novo sozinha")
			}
			if r.rb.Status().Recusada == "" {
				t.Fatal("o /status mostra o motivo")
			}
		})
	}
}

// AP-18: a reconstrução espera o applier ocioso antes de copiar.
func TestRebuildWaitsIdleApplier(t *testing.T) {
	r := newRebuildRig(bothTables)
	r.setIdle(false)
	done := r.run(t)
	r.mode.EnterRebuilding()
	time.Sleep(250 * time.Millisecond)
	if r.client.n() != 0 {
		t.Fatal("copiou com o applier ocupado")
	}
	r.setIdle(true)
	if err := waitErr(t, done); !errors.Is(err, ErrRestartAfterRebuild) {
		t.Fatal(err)
	}
}

// O-41: a líder não se reconstrói; na follower, o Trigger põe em reconstruindo.
func TestRebuildTrigger(t *testing.T) {
	r := newRebuildRig(bothTables)
	r.rb.IsLeader = func() bool { return true }
	if err := r.rb.Trigger(); !errors.Is(err, ErrLeaderRebuild) || r.mode.State() != domain.CellNormal {
		t.Fatalf("líder: %v %v", err, r.mode.State())
	}
	r.rb.IsLeader = func() bool { return false }
	if err := r.rb.Trigger(); err != nil || r.mode.State() != domain.CellRebuilding {
		t.Fatalf("follower: %v %v", err, r.mode.State())
	}
	if err := r.rb.Trigger(); err != nil || r.mode.State() != domain.CellRebuilding {
		t.Fatal("já em reconstruindo: sem efeito")
	}
}

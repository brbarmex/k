package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

type fakeStore struct {
	mu        sync.Mutex
	applied   []port.VersionedOp
	failAt    domain.Index
	transient int // quantas falhas transitórias devolver antes de aceitar
}

func (s *fakeStore) ApplyBatch(_ context.Context, ops []port.VersionedOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transient > 0 {
		s.transient--
		return fmt.Errorf("%w: limite de vazão", port.ErrStoreTransient)
	}
	for _, op := range ops {
		if op.Version == s.failAt {
			return fmt.Errorf("%w: item venenoso", port.ErrStorePermanent)
		}
		s.applied = append(s.applied, op)
	}
	return nil
}
func (*fakeStore) Get(context.Context, domain.LogicalTable, []byte) ([]byte, error) {
	return nil, nil
}
func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.applied)
}

type fakeProgress struct {
	mu    sync.Mutex
	idx   domain.Index
	ord   uint32
	saves int
}

func (p *fakeProgress) SaveProgress(i domain.Index, o uint32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idx, p.ord = i, o
	p.saves++
	return nil
}
func (p *fakeProgress) LoadProgress() (domain.Index, uint32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idx, p.ord, nil
}

func write(idx domain.Index, items int) domain.Entry {
	e := domain.Entry{Index: idx, Term: 1, Kind: domain.KindWrite}
	for i := 0; i < items; i++ {
		e.Ops = append(e.Ops, domain.Op{Table: "t_{cell}", Kind: domain.OpPut, Key: []byte{byte(i)}})
	}
	return e
}

func startApplier(t *testing.T, a *Applier) (context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	return cancel, done
}

func TestApplierNeverPassesTheGate(t *testing.T) {
	g := &domain.Gate{}
	in := make(chan []domain.Entry, 1)
	st := &fakeStore{}
	a := &Applier{Gate: g, Store: st, Progress: &fakeProgress{}, In: in}
	cancel, _ := startApplier(t, a)
	defer cancel()

	in <- []domain.Entry{write(1, 1), write(2, 1)}
	time.Sleep(20 * time.Millisecond)
	if st.count() != 0 {
		t.Fatalf("aplicou %d itens antes do gate", st.count())
	}
	releaseGate(g, 1)
	ctx, c2 := context.WithTimeout(context.Background(), time.Second)
	defer c2()
	if err := a.WaitApplied(ctx, 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if a.Applied() != 1 || st.count() != 1 {
		t.Fatalf("applied = %d, itens = %d; o gate estava em 1", a.Applied(), st.count())
	}
	releaseGate(g, 2)
	if err := a.WaitApplied(ctx, 2); err != nil {
		t.Fatal(err)
	}
}

func TestApplierResumesFromItemProgress(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 10)
	in := make(chan []domain.Entry, 1)
	st := &fakeStore{}
	// Antes da queda, a entrada 5 tinha os itens 1 e 2 de 3 aplicados.
	prog := &fakeProgress{idx: 5, ord: 2}
	a := &Applier{Gate: g, Store: st, Progress: prog, In: in}
	cancel, _ := startApplier(t, a)
	defer cancel()

	in <- []domain.Entry{write(4, 2), write(5, 3), write(6, 1)}
	ctx, c2 := context.WithTimeout(context.Background(), time.Second)
	defer c2()
	if err := a.WaitApplied(ctx, 6); err != nil {
		t.Fatal(err)
	}
	// Reaplica só (5, 3) e (6, 1): 4 e os dois primeiros itens de 5 já estavam no banco.
	if st.count() != 2 {
		t.Fatalf("itens reaplicados = %d, quer 2", st.count())
	}
	if idx, ord, _ := prog.LoadProgress(); idx != 6 || ord != 1 {
		t.Fatalf("progresso = (%d, %d), quer (6, 1)", idx, ord)
	}
}

// ADR-049, item 4: com o applier estacionado, esperar um índice no item venenoso ou depois
// dele falha na hora com ErrApplierStopped, inclusive para quem já esperava antes de
// estacionar; um índice anterior ao item venenoso continua sendo atendido.
func TestWaitAppliedFailsFastWhenParked(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 10)
	in := make(chan []domain.Entry, 1)
	a := &Applier{Gate: g, Store: &fakeStore{failAt: 2}, Progress: &fakeProgress{}, In: in}
	cancel, _ := startApplier(t, a)
	defer cancel()

	ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	early := make(chan error, 1)
	go func() { early <- a.WaitApplied(ctx, 3) }() // espera antes de o applier estacionar
	in <- []domain.Entry{write(1, 1), write(2, 1), write(3, 1)}

	start := time.Now()
	select {
	case err := <-early:
		if !errors.Is(err, ErrApplierStopped) {
			t.Fatalf("quem já esperava recebeu %v, quer ErrApplierStopped", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("quem já esperava não foi acordado quando o applier estacionou")
	}
	if err := a.WaitApplied(ctx, 2); !errors.Is(err, ErrApplierStopped) {
		t.Fatalf("WaitApplied(2) no item venenoso = %v, quer ErrApplierStopped", err)
	}
	if err := a.WaitApplied(ctx, 1); err != nil {
		t.Fatalf("WaitApplied(1), antes do item venenoso = %v, quer nil", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("as esperas levaram %v; deviam ser imediatas", d)
	}
}

// AP-07: erro permanente estaciona o applier sem pular e sem derrubar o processo.
func TestPoisonItemStopsTheShard(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 10)
	in := make(chan []domain.Entry, 1)
	st := &fakeStore{failAt: 2}
	a := &Applier{Gate: g, Store: st, Progress: &fakeProgress{}, In: in}
	cancel, done := startApplier(t, a)

	in <- []domain.Entry{write(1, 1), write(2, 1), write(3, 1)}
	deadline := time.Now().Add(time.Second)
	for !a.Health().Stopped {
		if time.Now().After(deadline) {
			t.Fatal("applier não parou no item venenoso")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if h := a.Health(); h.Index != 2 || h.Ord != 1 || h.Err == "" {
		t.Fatalf("saúde = %+v; devia parar em (2, 1) com o erro", h)
	}
	if a.Applied() != 1 || st.count() != 1 {
		t.Fatalf("applied = %d, itens = %d; devia parar em 2 sem pular", a.Applied(), st.count())
	}
	select {
	case err := <-done:
		t.Fatalf("o applier estacionado não pode sair sozinho: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	<-done
}

// AP-07: erro transitório é repetido com backoff até dar certo.
func TestTransientErrorIsRetried(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 10)
	in := make(chan []domain.Entry, 1)
	st := &fakeStore{transient: 2}
	a := &Applier{Gate: g, Store: st, Progress: &fakeProgress{}, In: in,
		Backoff: Backoff{Min: time.Millisecond, Max: 2 * time.Millisecond}}
	cancel, _ := startApplier(t, a)
	defer cancel()

	in <- []domain.Entry{write(1, 1), write(2, 1)}
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	if err := a.WaitApplied(ctx, 2); err != nil {
		t.Fatalf("não aplicou depois dos erros transitórios: %v", err)
	}
	if a.Health().Stopped || st.count() != 2 {
		t.Fatalf("saúde = %+v, itens = %d", a.Health(), st.count())
	}
}

// keyedWrite cria uma entrada com um item por chave dada.
func keyedWrite(idx domain.Index, keys ...string) domain.Entry {
	e := domain.Entry{Index: idx, Term: 1, Kind: domain.KindWrite}
	for _, k := range keys {
		e.Ops = append(e.Ops, domain.Op{Table: "t_{cell}", Kind: domain.OpPut, Key: []byte(k)})
	}
	return e
}

// gatedStore segura cada gravação até o teste liberar a chave, e mede quantas gravações
// estiveram no banco ao mesmo tempo.
type gatedStore struct {
	mu      sync.Mutex
	release map[string]chan struct{}
	order   map[string][]domain.Index // versões gravadas, por chave, na ordem de gravação
	now     int
	peak    int
}

func newGatedStore() *gatedStore {
	return &gatedStore{release: map[string]chan struct{}{}, order: map[string][]domain.Index{}}
}

func (s *gatedStore) gate(key string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.release[key]
	if !ok {
		ch = make(chan struct{})
		s.release[key] = ch
	}
	return ch
}

func (s *gatedStore) open(key string) { close(s.gate(key)) }

func (s *gatedStore) ApplyBatch(ctx context.Context, ops []port.VersionedOp) error {
	key := string(ops[0].Key)
	s.mu.Lock()
	s.now++
	s.peak = max(s.peak, s.now)
	s.mu.Unlock()
	select {
	case <-s.gate(key):
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.now--
	s.order[key] = append(s.order[key], ops[0].Version)
	s.mu.Unlock()
	return nil
}
func (*gatedStore) Get(context.Context, domain.LogicalTable, []byte) ([]byte, error) {
	return nil, nil
}
func (s *gatedStore) peakNow() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

// ADR-058: chaves diferentes são gravadas ao mesmo tempo, até o número de workers.
func TestApplierWritesDifferentKeysInParallel(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 100)
	in := make(chan []domain.Entry, 1)
	st := newGatedStore()
	a := &Applier{Gate: g, Store: st, Progress: &fakeProgress{}, In: in, Workers: 64}
	cancel, _ := startApplier(t, a)
	defer cancel()

	var batch []domain.Entry
	keys := make([]string, 8)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
		batch = append(batch, keyedWrite(domain.Index(i+1), keys[i]))
	}
	in <- batch
	// Com um item por vez, só uma gravação estaria no banco; com 64 workers e 8 chaves,
	// as que caem em workers diferentes ficam no banco juntas.
	waitUntil(t, func() bool { return st.peakNow() >= 2 }, "nenhuma gravação em paralelo")
	if a.Applied() != 0 {
		t.Fatalf("marca d'água = %d com tudo ainda no banco", a.Applied())
	}
	for _, k := range keys {
		st.open(k)
	}
	ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
	defer c()
	if err := a.WaitApplied(ctx, 8); err != nil {
		t.Fatal(err)
	}
}

// A marca d'água só passa de uma entrada quando ela e todas as anteriores estão no banco,
// e o progresso salvo é a marca d'água.
func TestWatermarkWaitsForTheSlowestEarlierKey(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 100)
	in := make(chan []domain.Entry, 1)
	st := newGatedStore()
	prog := &fakeProgress{}
	a := &Applier{Gate: g, Store: st, Progress: prog, In: in, Workers: 64}
	cancel, _ := startApplier(t, a)
	defer cancel()

	slot := func(k string) int { return keySlot(domain.Op{Table: "t_{cell}", Key: []byte(k)}, 64) }
	fast := "b"
	for i := 0; slot(fast) == slot("a"); i++ { // garante workers diferentes para "a" e a rápida
		fast = fmt.Sprintf("b%d", i)
	}
	in <- []domain.Entry{keyedWrite(1, "a"), keyedWrite(2, fast), {Index: 3, Term: 1, Kind: domain.KindNoop}}
	st.open(fast)
	waitUntil(t, func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return len(st.order[fast]) == 1
	}, "a entrada 2 não foi gravada")
	time.Sleep(20 * time.Millisecond)
	if a.Applied() != 0 {
		t.Fatalf("marca d'água = %d com a entrada 1 ainda no banco", a.Applied())
	}
	st.open("a")
	ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
	defer c()
	if err := a.WaitApplied(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if idx, ord, _ := prog.LoadProgress(); idx != 3 || ord != 0 {
		t.Fatalf("progresso = (%d, %d), quer (3, 0): a marca d'água", idx, ord)
	}
}

// A mesma chave é gravada na ordem do log, mesmo com muitos workers.
func TestSameKeyIsAppliedInLogOrder(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 1000)
	in := make(chan []domain.Entry, 1)
	st := newGatedStore()
	for _, k := range []string{"x", "y", "z"} {
		st.open(k)
	}
	a := &Applier{Gate: g, Store: st, Progress: &fakeProgress{}, In: in, Workers: 16}
	cancel, _ := startApplier(t, a)
	defer cancel()

	var batch []domain.Entry
	for i := 1; i <= 300; i++ {
		batch = append(batch, keyedWrite(domain.Index(i), []string{"x", "y", "z"}[i%3]))
	}
	in <- batch
	ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
	defer c()
	if err := a.WaitApplied(ctx, 300); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, vs := range st.order {
		for i := 1; i < len(vs); i++ {
			if vs[i] <= vs[i-1] {
				t.Fatalf("chave %s gravada fora de ordem: %v", k, vs)
			}
		}
	}
}

// Com workers em paralelo, o item venenoso para a marca d'água logo antes dele, os itens
// anteriores de outras chaves terminam e nenhum item posterior é gravado depois da parada.
func TestPoisonItemStopsTheWatermarkWithParallelWorkers(t *testing.T) {
	g := &domain.Gate{}
	releaseGate(g, 100)
	in := make(chan []domain.Entry, 1)
	st := &fakeStore{failAt: 4}
	a := &Applier{Gate: g, Store: st, Progress: &fakeProgress{}, In: in, Workers: 8}
	cancel, done := startApplier(t, a)

	var batch []domain.Entry
	for i := 1; i <= 3; i++ {
		batch = append(batch, keyedWrite(domain.Index(i), fmt.Sprintf("k%d", i)))
	}
	in <- batch
	ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
	defer c()
	if err := a.WaitApplied(ctx, 3); err != nil {
		t.Fatal(err)
	}
	in <- []domain.Entry{keyedWrite(4, "k4"), keyedWrite(5, "k5"), keyedWrite(6, "k4")}
	waitUntil(t, func() bool { return a.Health().Stopped }, "applier não parou no item venenoso")
	if h := a.Health(); h.Index != 4 || h.Ord != 1 {
		t.Fatalf("saúde = %+v; devia parar em (4, 1)", h)
	}
	time.Sleep(30 * time.Millisecond)
	if a.Applied() != 3 {
		t.Fatalf("marca d'água = %d; devia parar em 3", a.Applied())
	}
	st.mu.Lock()
	for _, op := range st.applied {
		if op.Version == 6 {
			t.Fatal("a entrada 6 (mesma chave do item venenoso) foi gravada")
		}
	}
	st.mu.Unlock()
	cancel()
	<-done
}

func TestKeySlotIsStableAndSpreads(t *testing.T) {
	op := func(table, key string) domain.Op {
		return domain.Op{Table: domain.LogicalTable(table), Key: []byte(key)}
	}
	if s := keySlot(op("t", "a"), 16); s < 0 || s >= 16 || s != keySlot(op("t", string([]byte{'a'})), 16) {
		t.Fatal("a mesma chave precisa cair sempre no mesmo worker, dentro da faixa")
	}
	if keySlot(op("t", "a"), 1) != 0 || keySlot(op("t", "a"), 0) != 0 {
		t.Fatal("com 1 worker tudo vai para o 0")
	}
	used := map[int]bool{}
	for i := 0; i < 200; i++ {
		used[keySlot(op("t", fmt.Sprintf("cli-%d", i)), 16)] = true
	}
	if len(used) < 12 {
		t.Fatalf("200 chaves usaram só %d de 16 workers", len(used))
	}
}

// slowStore simula a latência de um PutItem.
type slowStore struct{ d time.Duration }

func (s slowStore) ApplyBatch(ctx context.Context, _ []port.VersionedOp) error {
	t := time.NewTimer(s.d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (slowStore) Get(context.Context, domain.LogicalTable, []byte) ([]byte, error) { return nil, nil }

// BenchmarkApplierThroughput mede itens/s do applier com um banco simulado de 4 ms por
// item e chaves distintas (ADR-058): `go test -bench Applier -run ^$ ./internal/usecase`.
func BenchmarkApplierThroughput(b *testing.B) {
	for _, workers := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			const items = 2000
			g := &domain.Gate{}
			g.SetRaftCommit(items)
			for i := 0; i < b.N; i++ {
				in := make(chan []domain.Entry, 1)
				a := &Applier{Gate: g, Store: slowStore{4 * time.Millisecond}, Progress: &fakeProgress{}, In: in, Workers: workers}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- a.Run(ctx) }()
				batch := make([]domain.Entry, 0, items)
				for j := 1; j <= items; j++ {
					batch = append(batch, keyedWrite(domain.Index(j), fmt.Sprintf("cli-%d", j)))
				}
				start := time.Now()
				in <- batch
				if err := a.WaitApplied(context.Background(), items); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(items)/time.Since(start).Seconds(), "itens/s")
				cancel()
				<-done
			}
		})
	}
}

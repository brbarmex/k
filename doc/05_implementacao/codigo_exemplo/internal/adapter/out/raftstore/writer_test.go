package raftstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
)

// fakeStorage registra a ordem dos eventos e deixa o teste segurar o fsync.
type fakeStorage struct {
	mu       sync.Mutex
	events   []string
	persists [][]Record
	states   []*HardState
	hold     chan struct{} // se não nulo, Persist espera um valor aqui (o "fsync" em andamento)
	entered  chan struct{} // avisado quando Persist começa
	err      error
}

func (f *fakeStorage) log(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, s)
}

func (f *fakeStorage) Persist(st *HardState, ents []Record) error {
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.hold != nil {
		<-f.hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.persists = append(f.persists, append([]Record(nil), ents...))
	f.states = append(f.states, st)
	f.events = append(f.events, fmt.Sprintf("persist:%d", len(ents)))
	return nil
}

func (f *fakeStorage) Publish(*HardState, []Record) error {
	f.log("publish")
	return nil
}

func (f *fakeStorage) snapshot() ([]string, [][]Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...), append([][]Record(nil), f.persists...)
}

func rec(idx domain.Index, term domain.Term, size int) Record {
	return Record{Index: idx, Term: term, Data: make([]byte, size)}
}

func batch(st *fakeStorage, name string, done *sync.WaitGroup, rs ...Record) Batch {
	done.Add(1)
	return Batch{Entries: rs, Release: func() { st.log("release:" + name); done.Done() }}
}

func start(t *testing.T, st *fakeStorage, opt Options) (*Writer, context.CancelFunc, chan error) {
	t.Helper()
	if opt.Fatal == nil {
		opt.Fatal = func(err error) {}
	}
	w := New(st, opt)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- w.Run(ctx) }()
	t.Cleanup(cancel)
	return w, cancel, res
}

func wait(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("respostas não liberadas")
	}
}

// Nenhuma resposta sai antes do fsync e da publicação no MemoryStorage (ADR-056).
func TestReleaseOnlyAfterPersistAndPublish(t *testing.T) {
	st := &fakeStorage{hold: make(chan struct{}), entered: make(chan struct{}, 1)}
	w, _, _ := start(t, st, Options{})
	var wg sync.WaitGroup
	if err := w.Submit(context.Background(), batch(st, "a", &wg, rec(1, 1, 10))); err != nil {
		t.Fatal(err)
	}
	<-st.entered // fsync em andamento
	time.Sleep(20 * time.Millisecond)
	if ev, _ := st.snapshot(); len(ev) != 0 {
		t.Fatalf("algo saiu antes do fsync: %v", ev)
	}
	st.hold <- struct{}{}
	wait(t, &wg)
	ev, _ := st.snapshot()
	want := []string{"persist:1", "publish", "release:a"}
	if fmt.Sprint(ev) != fmt.Sprint(want) {
		t.Fatalf("ordem %v, esperado %v", ev, want)
	}
}

// O que chega enquanto o fsync roda vai junto no próximo, num único Persist, e as
// respostas saem na ordem de chegada.
func TestCoalescesWhatArrivesDuringFsync(t *testing.T) {
	st := &fakeStorage{hold: make(chan struct{}), entered: make(chan struct{}, 4)}
	w, _, _ := start(t, st, Options{})
	var wg sync.WaitGroup
	ctx := context.Background()
	_ = w.Submit(ctx, batch(st, "a", &wg, rec(1, 1, 10)))
	<-st.entered
	_ = w.Submit(ctx, batch(st, "b", &wg, rec(2, 1, 10)))
	_ = w.Submit(ctx, batch(st, "c", &wg, rec(3, 1, 10), rec(4, 1, 10)))
	_ = w.Submit(ctx, batch(st, "d", &wg, rec(5, 1, 10)))
	time.Sleep(20 * time.Millisecond) // os três já estão na fila
	st.hold <- struct{}{}
	<-st.entered
	st.hold <- struct{}{}
	wait(t, &wg)
	ev, ps := st.snapshot()
	if len(ps) != 2 || len(ps[0]) != 1 || len(ps[1]) != 4 {
		t.Fatalf("esperado 2 fsyncs (1 e 4 entradas), veio %v", ev)
	}
	want := "[persist:1 publish release:a persist:4 publish release:b release:c release:d]"
	if fmt.Sprint(ev) != want {
		t.Fatalf("ordem %v", ev)
	}
}

// Sem espera fixa: com tráfego baixo, uma entrada é gravada na hora, sozinha.
func TestNoLingerWhenIdle(t *testing.T) {
	st := &fakeStorage{}
	w, _, _ := start(t, st, Options{})
	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		_ = w.Submit(context.Background(), batch(st, fmt.Sprint(i), &wg, rec(domain.Index(i), 1, 10)))
		wait(t, &wg) // espera a resposta antes de mandar a próxima, como um cliente sozinho
	}
	if _, ps := st.snapshot(); len(ps) != 3 {
		t.Fatalf("esperado um fsync por escrita isolada, veio %d", len(ps))
	}
}

// O lote respeita o teto de bytes; uma entrada maior que o teto vai sozinha.
func TestBatchByteCap(t *testing.T) {
	st := &fakeStorage{hold: make(chan struct{}), entered: make(chan struct{}, 8)}
	w, _, _ := start(t, st, Options{MaxBatchBytes: 1 << 20})
	var wg sync.WaitGroup
	ctx := context.Background()
	_ = w.Submit(ctx, batch(st, "0", &wg, rec(1, 1, 10)))
	<-st.entered
	_ = w.Submit(ctx, batch(st, "1", &wg, rec(2, 1, 400<<10)))
	_ = w.Submit(ctx, batch(st, "2", &wg, rec(3, 1, 400<<10)))
	_ = w.Submit(ctx, batch(st, "3", &wg, rec(4, 1, 400<<10))) // não cabe com os dois anteriores
	_ = w.Submit(ctx, batch(st, "big", &wg, rec(5, 1, 4<<20))) // maior que o teto: sozinha
	_ = w.Submit(ctx, batch(st, "4", &wg, rec(6, 1, 10)))
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 5; i++ {
		st.hold <- struct{}{}
		if i < 4 {
			<-st.entered
		}
	}
	wait(t, &wg)
	_, ps := st.snapshot()
	sizes := []int{}
	for _, p := range ps {
		sizes = append(sizes, len(p))
	}
	if fmt.Sprint(sizes) != "[1 2 1 1 1]" {
		t.Fatalf("lotes %v, esperado [1 2 1 1 1]", sizes)
	}
	for i, p := range ps {
		n := 0
		for _, r := range p {
			n += len(r.Data)
		}
		if len(p) > 1 && n > 1<<20 {
			t.Fatalf("lote %d com %d bytes passou do teto", i, n)
		}
	}
}

// Snapshot (Barrier) nunca é juntado com outros lotes.
func TestBarrierGoesAlone(t *testing.T) {
	st := &fakeStorage{hold: make(chan struct{}), entered: make(chan struct{}, 8)}
	w, _, _ := start(t, st, Options{})
	var wg sync.WaitGroup
	ctx := context.Background()
	_ = w.Submit(ctx, batch(st, "a", &wg, rec(1, 1, 10)))
	<-st.entered
	_ = w.Submit(ctx, batch(st, "b", &wg, rec(2, 1, 10)))
	snap := batch(st, "snap", &wg)
	snap.Barrier = true
	_ = w.Submit(ctx, snap)
	_ = w.Submit(ctx, batch(st, "c", &wg, rec(3, 1, 10)))
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 4; i++ {
		st.hold <- struct{}{}
		if i < 3 {
			<-st.entered
		}
	}
	wait(t, &wg)
	ev, _ := st.snapshot()
	want := "[persist:1 publish release:a persist:1 publish release:b persist:0 publish release:snap persist:1 publish release:c]"
	if fmt.Sprint(ev) != want {
		t.Fatalf("ordem %v", ev)
	}
}

// Erro de fsync é fatal e nenhuma resposta do grupo é liberada (ADR-034).
func TestFsyncErrorIsFatalAndReleasesNothing(t *testing.T) {
	st := &fakeStorage{err: errors.New("EIO")}
	var fatal error
	var mu sync.Mutex
	w, _, res := start(t, st, Options{Fatal: func(err error) { mu.Lock(); fatal = err; mu.Unlock() }})
	var released bool
	_ = w.Submit(context.Background(), Batch{Entries: []Record{rec(1, 1, 10)}, Release: func() { released = true }})
	select {
	case err := <-res:
		if err == nil {
			t.Fatal("Run deveria devolver o erro")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run não parou")
	}
	mu.Lock()
	defer mu.Unlock()
	if fatal == nil {
		t.Fatal("Fatal não foi chamado")
	}
	if released {
		t.Fatal("resposta liberada depois de erro de fsync")
	}
}

// Troca de termo: o lote novo reescreve índices do anterior e o último HardState vale.
func TestMergeTruncatesRewrittenIndices(t *testing.T) {
	s1 := &HardState{Term: 1, Vote: "a1", Commit: 4}
	s2 := &HardState{Term: 2, Vote: "a2", Commit: 4}
	group := []Batch{
		{Entries: []Record{rec(5, 1, 1), rec(6, 1, 1), rec(7, 1, 1)}, State: s1},
		{Entries: []Record{rec(6, 2, 1), rec(7, 2, 1)}, State: s2},
		{State: nil},
	}
	st, ents, err := Merge(group)
	if err != nil {
		t.Fatal(err)
	}
	if st != s2 {
		t.Fatalf("HardState %+v, esperado o último", st)
	}
	got := ""
	for _, r := range ents {
		got += fmt.Sprintf("%d/%d ", r.Index, r.Term)
	}
	if got != "5/1 6/2 7/2 " {
		t.Fatalf("entradas %q", got)
	}

	// Reescrita a partir do começo do grupo.
	_, ents, _ = Merge([]Batch{{Entries: []Record{rec(5, 1, 1), rec(6, 1, 1)}}, {Entries: []Record{rec(5, 3, 1)}}})
	if len(ents) != 1 || ents[0].Term != 3 {
		t.Fatalf("esperado só 5/3, veio %v", ents)
	}
}

func TestMergeRefusesGap(t *testing.T) {
	_, _, err := Merge([]Batch{{Entries: []Record{rec(5, 1, 1)}}, {Entries: []Record{rec(7, 1, 1)}}})
	if !errors.Is(err, ErrGap) {
		t.Fatalf("esperado ErrGap, veio %v", err)
	}
}

// Merge não altera os slices de quem chamou.
func TestMergeDoesNotAliasInput(t *testing.T) {
	a := []Record{rec(1, 1, 1), rec(2, 1, 1)}
	_, ents, _ := Merge([]Batch{{Entries: a}, {Entries: []Record{rec(2, 2, 1)}}})
	if a[1].Term != 1 || ents[1].Term != 2 {
		t.Fatalf("entrada do chamador alterada: %v", a)
	}
}

// Muitos produtores em sequência com o escritor rodando: tudo é gravado uma vez, em
// ordem, e cada resposta sai uma única vez (go test -race).
func TestManyBatchesInOrder(t *testing.T) {
	st := &fakeStorage{}
	var count int
	var mu sync.Mutex
	w, _, _ := start(t, st, Options{OnBatch: func(n, _ int) { mu.Lock(); count += n; mu.Unlock() }})
	var wg sync.WaitGroup
	for i := 1; i <= 500; i++ {
		_ = w.Submit(context.Background(), batch(st, fmt.Sprint(i), &wg, rec(domain.Index(i), 1, 64)))
	}
	wait(t, &wg)
	_, ps := st.snapshot()
	next := domain.Index(1)
	for _, p := range ps {
		for _, r := range p {
			if r.Index != next {
				t.Fatalf("índice %d fora de ordem, esperado %d", r.Index, next)
			}
			next++
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if next != 501 || count != 500 {
		t.Fatalf("gravadas %d, contadas %d", next-1, count)
	}
}

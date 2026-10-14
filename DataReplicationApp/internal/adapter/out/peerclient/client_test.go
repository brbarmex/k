package peerclient

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	"example.com/datareplication/internal/adapter/in/peergrpc"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

type fakeRaft struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (f *fakeRaft) Step(_ context.Context, m [][]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, m...)
	return nil
}

func (f *fakeRaft) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.msgs)
}

type fakeWriter struct {
	res port.WriteResult
	err error
	got port.WriteRequest
}

func (f *fakeWriter) Write(_ context.Context, r port.WriteRequest) (port.WriteResult, error) {
	f.got = r
	return f.res, f.err
}

type fakeBarrier struct {
	ri, gate domain.Index
	err      error
}

func (f fakeBarrier) LeaderBarrier(context.Context) (domain.Index, domain.Index, error) {
	return f.ri, f.gate, f.err
}

type fakeGate struct {
	mu   sync.Mutex
	last domain.Index
}

func (f *fakeGate) OnGateUpdate(_ domain.Term, g domain.Index) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = g
}

func (f *fakeGate) get() domain.Index {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func setup(t *testing.T, srv *peergrpc.Server) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
	c, err := New(1, map[uint64]string{1: "self", 2: "passthrough:///peer2"}, time.Second, zerolog.Nop(), dialer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

// Ida e volta dos quatro métodos (O-04).
func TestRoundTrip(t *testing.T) {
	raft, w, g := &fakeRaft{}, &fakeWriter{res: port.WriteResult{Index: 42}}, &fakeGate{}
	c := setup(t, &peergrpc.Server{Raft: raft, Writer: w, Barrier: fakeBarrier{ri: 7, gate: 5}, Gate: g})

	if !c.Send(2, []byte("m1")) || !c.Send(2, []byte("m2")) {
		t.Fatal("Send recusou com fila vazia")
	}
	if c.Send(9, []byte("x")) {
		t.Fatal("par desconhecido devia ser recusado")
	}
	deadline := time.Now().Add(2 * time.Second)
	for raft.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if raft.count() != 2 {
		t.Fatalf("Step entregou %d mensagens", raft.count())
	}

	m := &recMetrics{}
	c.Metrics = m
	ops := []domain.Op{{Table: "t_{cell}", Kind: domain.OpPut, Key: []byte(`{"pk":{"S":"a"}}`), Item: []byte(`{"pk":{"S":"a"}}`)}}
	res, err := c.Forward(context.Background(), "2", port.WriteRequest{Ops: ops})
	if err != nil || res.Index != 42 || !w.got.Forwarded || string(w.got.Ops[0].Key) != `{"pk":{"S":"a"}}` {
		t.Fatalf("Forward = %+v %v; recebido %+v", res, err, w.got)
	}

	if m.n("forwarded") != 1 {
		t.Fatalf("encaminhamentos registrados = %d, quer 1", m.n("forwarded"))
	}
	ri, gate, err := c.Barrier(context.Background(), "2")
	if err != nil || ri != 7 || gate != 5 {
		t.Fatalf("Barrier = %d %d %v", ri, gate, err)
	}

	for i := domain.Index(1); i <= 20; i++ {
		c.Publish(3, i)
	}
	deadline = time.Now().Add(2 * time.Second)
	for g.get() != 20 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if g.get() != 20 {
		t.Fatalf("o último gate publicado não chegou: %d", g.get())
	}
}

// O Forward preserva a classe do erro da líder (RF-10).
func TestForwardPreservesErrorClass(t *testing.T) {
	w := &fakeWriter{}
	c := setup(t, &peergrpc.Server{Raft: &fakeRaft{}, Writer: w, Barrier: fakeBarrier{err: usecase.ErrNotLeader}, Gate: &fakeGate{}})
	for _, want := range []error{usecase.ErrNoLeader, usecase.ErrPending, usecase.ErrBackpressure, usecase.ErrLeaseExpired} {
		w.err = want
		if _, err := c.Forward(context.Background(), "2", port.WriteRequest{}); !errors.Is(err, want) {
			t.Fatalf("Forward devolveu %v, quer %v", err, want)
		}
	}
	if _, _, err := c.Barrier(context.Background(), "2"); !errors.Is(err, usecase.ErrNoLeader) {
		t.Fatalf("barreira numa ex-líder = %v, quer ErrNoLeader", err)
	}
	if _, err := c.Forward(context.Background(), "9", port.WriteRequest{}); !errors.Is(err, usecase.ErrNoLeader) {
		t.Fatalf("líder desconhecida = %v", err)
	}
}

// Fila cheia descarta e devolve false (SG-10).
func TestSendDropsWhenQueueIsFull(t *testing.T) {
	m := &recMetrics{}
	c := &Client{peers: map[uint64]*peer{2: {id: 2, raftQ: make(chan []byte, 1)}}, Metrics: m}
	if !c.Send(2, []byte("a")) || c.Send(2, []byte("b")) {
		t.Fatal("a segunda mensagem devia ser descartada")
	}
	if m.n("dropped") != 1 {
		t.Fatalf("descartes registrados = %d, quer 1", m.n("dropped"))
	}
}

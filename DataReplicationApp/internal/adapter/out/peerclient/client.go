// Package peerclient é o cliente gRPC entre células (RT-12): transporte das mensagens do
// Raft, encaminhamento da escrita à líder, barreira de leitura e publicação do gate.
package peerclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"example.com/datareplication/api/peerv1"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

const (
	raftQueueSize = 4096 // mensagens do Raft por par; cheia = descarta (SG-10)
	stepBatch     = 256  // mensagens por chamada Step
	callTimeout   = time.Second
)

// Client implementa etcdraft.Sender, port.Forwarder, port.BarrierClient,
// port.GatePublisher e port.RecoveryClient.
type Client struct {
	self           uint64
	forwardTimeout time.Duration
	queueBytes     int64 // PEER_QUEUE_BYTES: teto em bytes da fila de cada par; 0 = sem teto (DC-09)
	log            zerolog.Logger
	peers          map[uint64]*peer
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup

	// Metrics recebe os descartes e os encaminhamentos (O-16). Definir antes do uso;
	// nulo = sem métricas.
	Metrics port.Metrics
	// AppliedSink recebe o aplicado das followers (DC-15). Definir antes do uso; nulo =
	// ignorado.
	AppliedSink port.PeerAppliedSink
}

var (
	_ port.Forwarder      = (*Client)(nil)
	_ port.BarrierClient  = (*Client)(nil)
	_ port.GatePublisher  = (*Client)(nil)
	_ port.RecoveryClient = (*Client)(nil)
)

type peer struct {
	id     uint64
	conn   *grpc.ClientConn
	rpc    peerv1.Client
	raftQ  chan []byte
	queued atomic.Int64 // bytes na raftQ (DC-09)
	gateMu sync.Mutex
	gate   *peerv1.GateUpdateRequest // só o valor mais recente
	gateC  chan struct{}
}

// New cria um cliente para cada par (o próprio nó fica de fora). Sem TLS na POC.
// queueBytes limita os bytes na fila de saída de cada par (PEER_QUEUE_BYTES, DC-09), além
// das 4.096 mensagens. extra acrescenta opções de conexão (os testes usam um dialer em
// memória).
func New(self uint64, peers map[uint64]string, forwardTimeout time.Duration, queueBytes int64, log zerolog.Logger, extra ...grpc.DialOption) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{self: self, forwardTimeout: forwardTimeout, queueBytes: queueBytes, log: log, peers: map[uint64]*peer{}, ctx: ctx, cancel: cancel}
	for id, addr := range peers {
		if id == self {
			continue
		}
		opts := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, peerv1.DialOptions()...)
		opts = append(opts, extra...)
		conn, err := grpc.NewClient(addr, opts...)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("peerclient: par %d (%s): %w", id, addr, err)
		}
		p := &peer{id: id, conn: conn, rpc: peerv1.NewClient(conn), raftQ: make(chan []byte, raftQueueSize), gateC: make(chan struct{}, 1)}
		c.peers[id] = p
		c.wg.Add(2)
		go c.pumpRaft(p)
		go c.pumpGate(p)
	}
	return c, nil
}

// Send enfileira a mensagem do Raft para o par, sem bloquear. Fila cheia (em mensagens ou
// em bytes, DC-09) ou par desconhecido devolve false, e o nó avisa o Raft
// (ReportUnreachable).
func (c *Client) Send(to uint64, msg []byte) bool {
	p := c.peers[to]
	if p == nil {
		return false
	}
	n := int64(len(msg))
	if c.queueBytes > 0 && p.queued.Add(n) > c.queueBytes {
		p.queued.Add(-n)
		port.MetricsOrNop(c.Metrics).PeerSendDropped(to)
		return false
	} else if c.queueBytes <= 0 {
		p.queued.Add(n)
	}
	select {
	case p.raftQ <- msg:
		return true
	default:
		p.queued.Add(-n)
		port.MetricsOrNop(c.Metrics).PeerSendDropped(to)
		return false
	}
}

// dequeued devolve à conta da fila os bytes de uma mensagem que saiu dela.
func (c *Client) dequeued(p *peer, msg []byte) { p.queued.Add(-int64(len(msg))) }

func (c *Client) pumpRaft(p *peer) {
	defer c.wg.Done()
	for {
		var first []byte
		select {
		case <-c.ctx.Done():
			return
		case first = <-p.raftQ:
		}
		c.dequeued(p, first)
		batch := [][]byte{first}
	drain:
		for len(batch) < stepBatch {
			select {
			case m := <-p.raftQ:
				c.dequeued(p, m)
				batch = append(batch, m)
			default:
				break drain
			}
		}
		ctx, cancel := context.WithTimeout(c.ctx, callTimeout)
		if _, err := p.rpc.Step(ctx, &peerv1.StepRequest{Msgs: batch}); err != nil {
			c.log.Debug().Err(err).Uint64("par", p.id).Int("mensagens", len(batch)).Msg("envio ao par falhou")
		}
		cancel()
	}
}

// Forward leva a escrita inteira à líder (um salto, ADR-010) e reconstrói a classe do
// resultado. Falha de transporte ou prazo vira ErrPending: a escrita pode ter sido proposta.
func (c *Client) Forward(ctx context.Context, leader string, r port.WriteRequest) (port.WriteResult, error) {
	p, err := c.peerOf(leader)
	if err != nil {
		return port.WriteResult{}, usecase.ErrNoLeader
	}
	c.log.Info().Str("lider", leader).Msg("escrita encaminhada à líder")
	port.MetricsOrNop(c.Metrics).Forwarded()
	ctx, cancel := context.WithTimeout(ctx, c.forwardTimeout)
	defer cancel()
	resp, err := p.rpc.Forward(ctx, &peerv1.ForwardRequest{Ops: r.Ops})
	if err != nil {
		return port.WriteResult{}, usecase.ErrPending
	}
	if err := peerv1.ErrOf(resp.Code); err != nil {
		return port.WriteResult{}, err
	}
	return port.WriteResult{Index: domain.Index(resp.Index)}, nil
}

// Barrier pergunta à líder ReadIndex e gate na mesma resposta (A.6, R16).
func (c *Client) Barrier(ctx context.Context, leader string) (domain.Index, domain.Index, error) {
	p, err := c.peerOf(leader)
	if err != nil {
		return 0, 0, usecase.ErrNoLeader
	}
	resp, err := p.rpc.ReadBarrier(ctx, &peerv1.BarrierRequest{})
	if err != nil {
		return 0, 0, usecase.ErrReadTimeout
	}
	switch err := peerv1.ErrOf(resp.Code); {
	case err == nil:
		return domain.Index(resp.ReadIndex), domain.Index(resp.Gate), nil
	case err == usecase.ErrNotLeader:
		return 0, 0, usecase.ErrNoLeader
	default:
		return 0, 0, err
	}
}

// Publish guarda o gate e o piso mais recentes para cada par e acorda o envio. Nunca
// bloqueia e nunca perde o último valor (G-06, RT-15).
func (c *Client) Publish(gate, floor domain.Index) {
	for _, p := range c.peers {
		p.gateMu.Lock()
		p.gate = &peerv1.GateUpdateRequest{Gate: uint64(gate), Floor: uint64(floor), To: p.id}
		p.gateMu.Unlock()
		select {
		case p.gateC <- struct{}{}:
		default:
		}
	}
}

func (c *Client) pumpGate(p *peer) {
	defer c.wg.Done()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-p.gateC:
		}
		p.gateMu.Lock()
		req := p.gate
		p.gateMu.Unlock()
		if req == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(c.ctx, callTimeout)
		resp, err := p.rpc.GateUpdate(ctx, req)
		if err != nil {
			c.log.Debug().Err(err).Uint64("par", p.id).Msg("gate não chegou ao par")
		} else if resp != nil && resp.Applied != nil && c.AppliedSink != nil {
			c.AppliedSink.PeerApplied(p.id, domain.Index(*resp.Applied), time.Now())
		}
		cancel()
	}
}

// Retained pergunta ao par o que ele retém (RT-19), com prazo de 1 s.
func (c *Client) Retained(ctx context.Context, node uint64) (port.RetainedInfo, error) {
	p := c.peers[node]
	if p == nil {
		return port.RetainedInfo{}, fmt.Errorf("%w: par %d desconhecido", port.ErrSourceGone, node)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	r, err := p.rpc.Retained(ctx, &peerv1.RetainedRequest{})
	if err != nil {
		return port.RetainedInfo{}, fmt.Errorf("%w: %v", port.ErrSourceGone, err)
	}
	if err := peerv1.ErrOf(r.Code); err != nil {
		return port.RetainedInfo{}, err
	}
	if r.Node != 0 && r.Node != node { // DC-19: a conexão caiu noutra célula
		return port.RetainedInfo{}, fmt.Errorf("%w: o par %d respondeu como nó %d", port.ErrSourceGone, node, r.Node)
	}
	return port.RetainedInfo{Node: r.Node, FirstRetained: domain.Index(r.FirstRetained), Commit: domain.Index(r.Commit),
		Gate: domain.Index(r.Gate), State: r.State, Leader: r.Leader, ClusterID: r.ClusterID, Epoch: r.Epoch,
		EmptyVolume: r.EmptyVolume, BootToken: r.BootToken, Genesis: r.Genesis}, nil
}

// CatchUp abre o fluxo das entradas de from a to no par (RT-19).
func (c *Client) CatchUp(ctx context.Context, node uint64, from, to domain.Index) (port.EntryStream, error) {
	p := c.peers[node]
	if p == nil {
		return nil, fmt.Errorf("%w: par %d desconhecido", port.ErrSourceGone, node)
	}
	ctx, cancel := context.WithCancel(ctx)
	s, err := p.rpc.CatchUp(ctx, &peerv1.CatchUpRequest{From: uint64(from), To: uint64(to), Owner: c.self, Node: node})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %v", port.ErrSourceGone, err)
	}
	return &entryStream{s: s, cancel: cancel}, nil
}

// CopyTable abre o fluxo da cópia das tabelas no par (AP-18).
func (c *Client) CopyTable(ctx context.Context, node uint64, tables []domain.LogicalTable) (port.CopyStream, error) {
	p := c.peers[node]
	if p == nil {
		return nil, fmt.Errorf("%w: par %d desconhecido", port.ErrSourceGone, node)
	}
	ctx, cancel := context.WithCancel(ctx)
	s, err := p.rpc.CopyTable(ctx, &peerv1.CopyTableRequest{Tables: tables, Owner: c.self, Node: node})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %v", port.ErrSourceGone, err)
	}
	return &copyStream{s: s, cancel: cancel}, nil
}

// recvErr traduz o erro do fluxo: io.EOF passa; o resto é a fonte que caiu.
func recvErr(err error) error {
	if errors.Is(err, io.EOF) {
		return io.EOF
	}
	return fmt.Errorf("%w: %v", port.ErrSourceGone, err)
}

type entryStream struct {
	s      peerv1.ClientStream[peerv1.CatchUpBatch]
	cancel context.CancelFunc
}

func (e *entryStream) Recv() (port.CatchUpBatch, error) {
	b, err := e.s.Recv()
	if err != nil {
		return port.CatchUpBatch{}, recvErr(err)
	}
	if err := peerv1.ErrOf(b.Code); err != nil {
		return port.CatchUpBatch{}, err
	}
	return port.CatchUpBatch{Entries: b.Entries, SourceGate: domain.Index(b.SourceGate)}, nil
}

func (e *entryStream) Close() { e.cancel() }

type copyStream struct {
	s      peerv1.ClientStream[peerv1.CopyTablePage]
	cancel context.CancelFunc
}

func (c *copyStream) Recv() (port.CopyPage, error) {
	p, err := c.s.Recv()
	if err != nil {
		return port.CopyPage{}, recvErr(err)
	}
	if err := peerv1.ErrOf(p.Code); err != nil {
		return port.CopyPage{}, err
	}
	out := port.CopyPage{F: domain.Index(p.F), Table: p.Table, KeySchema: p.KeySchema, TableDone: p.TableDone, Done: p.Done,
		Items: make([]port.VersionedItem, len(p.Items))}
	for i, it := range p.Items {
		out.Items[i] = port.VersionedItem{Key: it.Key, Item: it.Item, Version: domain.Index(it.Version)}
	}
	return out, nil
}

func (c *copyStream) Close() { c.cancel() }

func (c *Client) peerOf(node string) (*peer, error) {
	id, err := strconv.ParseUint(node, 10, 64)
	if err != nil {
		return nil, err
	}
	p := c.peers[id]
	if p == nil {
		return nil, fmt.Errorf("peerclient: par %d desconhecido", id)
	}
	return p, nil
}

// Close para as goroutines e fecha as conexões.
func (c *Client) Close() {
	c.cancel()
	c.wg.Wait()
	for _, p := range c.peers {
		_ = p.conn.Close()
	}
}

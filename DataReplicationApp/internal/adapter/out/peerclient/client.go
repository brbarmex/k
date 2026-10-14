// Package peerclient é o cliente gRPC entre células (RT-12): transporte das mensagens do
// Raft, encaminhamento da escrita à líder, barreira de leitura e publicação do gate.
package peerclient

import (
	"context"
	"fmt"
	"strconv"
	"sync"
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

// Client implementa etcdraft.Sender, port.Forwarder, port.BarrierClient e
// port.GatePublisher.
type Client struct {
	self           uint64
	forwardTimeout time.Duration
	log            zerolog.Logger
	peers          map[uint64]*peer
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup

	// Metrics recebe os descartes e os encaminhamentos (O-16). Definir antes do uso;
	// nulo = sem métricas.
	Metrics port.Metrics
}

var (
	_ port.Forwarder     = (*Client)(nil)
	_ port.BarrierClient = (*Client)(nil)
	_ port.GatePublisher = (*Client)(nil)
)

type peer struct {
	id     uint64
	conn   *grpc.ClientConn
	rpc    peerv1.Client
	raftQ  chan []byte
	gateMu sync.Mutex
	gate   *peerv1.GateUpdateRequest // só o valor mais recente
	gateC  chan struct{}
}

// New cria um cliente para cada par (o próprio nó fica de fora). Sem TLS na POC. extra
// acrescenta opções de conexão (os testes usam um dialer em memória).
func New(self uint64, peers map[uint64]string, forwardTimeout time.Duration, log zerolog.Logger, extra ...grpc.DialOption) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{self: self, forwardTimeout: forwardTimeout, log: log, peers: map[uint64]*peer{}, ctx: ctx, cancel: cancel}
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

// Send enfileira a mensagem do Raft para o par, sem bloquear. Fila cheia ou par
// desconhecido devolve false, e o nó avisa o Raft (ReportUnreachable).
func (c *Client) Send(to uint64, msg []byte) bool {
	p := c.peers[to]
	if p == nil {
		return false
	}
	select {
	case p.raftQ <- msg:
		return true
	default:
		port.MetricsOrNop(c.Metrics).PeerSendDropped(to)
		return false
	}
}

func (c *Client) pumpRaft(p *peer) {
	defer c.wg.Done()
	for {
		var first []byte
		select {
		case <-c.ctx.Done():
			return
		case first = <-p.raftQ:
		}
		batch := [][]byte{first}
	drain:
		for len(batch) < stepBatch {
			select {
			case m := <-p.raftQ:
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
	resp, err := p.rpc.Forward(ctx, &peerv1.ForwardRequest{Ops: r.Ops, ClientID: r.ClientID, Deadline: r.Deadline})
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

// Publish guarda o gate mais recente para cada par e acorda o envio. Nunca bloqueia e
// nunca perde o último valor (G-06).
func (c *Client) Publish(term domain.Term, gate domain.Index) {
	for _, p := range c.peers {
		p.gateMu.Lock()
		p.gate = &peerv1.GateUpdateRequest{Term: uint64(term), Gate: uint64(gate), From: c.self}
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
		if _, err := p.rpc.GateUpdate(ctx, req); err != nil {
			c.log.Debug().Err(err).Uint64("par", p.id).Msg("gate não chegou ao par")
		}
		cancel()
	}
}

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

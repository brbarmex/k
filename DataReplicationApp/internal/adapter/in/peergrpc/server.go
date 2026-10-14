// Package peergrpc é o servidor gRPC entre células (RT-12): recebe mensagens do Raft,
// escritas encaminhadas, pedidos de barreira de leitura e o gate publicado pela líder.
// Só traduz protocolo.
package peergrpc

import (
	"context"
	"net"
	"sync"

	"google.golang.org/grpc"

	"example.com/datareplication/api/peerv1"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// LeaderBarrier é a barreira de leitura servida pela líder (usecase.ReadService).
type LeaderBarrier interface {
	LeaderBarrier(ctx context.Context) (readIndex, gate domain.Index, err error)
}

// GateReceiver recebe o gate publicado pela líder (usecase.GateSync).
type GateReceiver interface {
	OnGateUpdate(term domain.Term, gate domain.Index)
}

// Server implementa peerv1.PeerServer.
type Server struct {
	Raft    port.RaftInbound
	Writer  port.Writer
	Barrier LeaderBarrier
	Gate    GateReceiver

	once sync.Once
	srv  *grpc.Server
}

func (s *Server) grpcServer() *grpc.Server {
	s.once.Do(func() {
		s.srv = grpc.NewServer(peerv1.ServerOptions()...)
		peerv1.RegisterPeerServer(s.srv, s)
	})
	return s.srv
}

var _ peerv1.PeerServer = (*Server)(nil)

func (s *Server) Step(ctx context.Context, req *peerv1.StepRequest) (*peerv1.Empty, error) {
	return &peerv1.Empty{}, s.Raft.Step(ctx, req.Msgs)
}

// Forward executa na líder o mesmo caminho da escrita local; a requisição vem marcada
// como encaminhada e nunca é reencaminhada (um salto, ADR-010).
func (s *Server) Forward(ctx context.Context, req *peerv1.ForwardRequest) (*peerv1.ForwardResponse, error) {
	res, err := s.Writer.Write(ctx, port.WriteRequest{Ops: req.Ops, ClientID: req.ClientID, Deadline: req.Deadline, Forwarded: true})
	out := &peerv1.ForwardResponse{Code: peerv1.CodeOf(err), Index: uint64(res.Index)}
	if err != nil {
		out.Message = err.Error()
	}
	return out, nil
}

func (s *Server) ReadBarrier(ctx context.Context, _ *peerv1.BarrierRequest) (*peerv1.BarrierResponse, error) {
	ri, gate, err := s.Barrier.LeaderBarrier(ctx)
	return &peerv1.BarrierResponse{Code: peerv1.CodeOf(err), ReadIndex: uint64(ri), Gate: uint64(gate)}, nil
}

func (s *Server) GateUpdate(_ context.Context, req *peerv1.GateUpdateRequest) (*peerv1.Empty, error) {
	s.Gate.OnGateUpdate(domain.Term(req.Term), domain.Index(req.Gate))
	return &peerv1.Empty{}, nil
}

// Serve abre o servidor gRPC no listener (sem TLS na POC).
func (s *Server) Serve(lis net.Listener) error {
	return s.grpcServer().Serve(lis)
}

// Stop encerra o servidor, esperando as chamadas em curso.
func (s *Server) Stop() {
	s.grpcServer().GracefulStop()
}

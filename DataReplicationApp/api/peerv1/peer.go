package peerv1

import (
	"context"

	"google.golang.org/grpc"

	"example.com/datareplication/internal/domain"
)

// ServiceName é o nome do serviço gRPC entre células.
const ServiceName = "datareplication.peer.v1.Peer"

// StepRequest leva um lote de mensagens do Raft já serializadas.
type StepRequest struct {
	Msgs [][]byte
}

// Empty é a resposta sem conteúdo.
type Empty struct{}

// ForwardRequest é a escrita inteira encaminhada pela follower à líder (ADR-010).
type ForwardRequest struct {
	Ops []domain.Op
}

// ForwardResponse traz a classe do resultado e, no OK, o índice commitado.
type ForwardResponse struct {
	Code  string
	Index uint64
}

// BarrierRequest pede a barreira de leitura à líder.
type BarrierRequest struct{}

// BarrierResponse traz ReadIndex e gate na MESMA resposta (A.6, R16).
type BarrierResponse struct {
	Code      string
	ReadIndex uint64
	Gate      uint64
}

// GateUpdateRequest é o gate publicado pela líder (G-06).
type GateUpdateRequest struct {
	Gate uint64
}

// PeerServer é o servidor entre células.
type PeerServer interface {
	Step(ctx context.Context, req *StepRequest) (*Empty, error)
	Forward(ctx context.Context, req *ForwardRequest) (*ForwardResponse, error)
	ReadBarrier(ctx context.Context, req *BarrierRequest) (*BarrierResponse, error)
	GateUpdate(ctx context.Context, req *GateUpdateRequest) (*Empty, error)
}

func handler[Req any, Resp any](name string, call func(PeerServer, context.Context, *Req) (*Resp, error)) grpc.MethodDesc {
	return grpc.MethodDesc{
		MethodName: name,
		Handler: func(srv any, ctx context.Context, dec func(any) error, ic grpc.UnaryServerInterceptor) (any, error) {
			req := new(Req)
			if err := dec(req); err != nil {
				return nil, err
			}
			if ic == nil {
				return call(srv.(PeerServer), ctx, req)
			}
			info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + ServiceName + "/" + name}
			return ic(ctx, req, info, func(ctx context.Context, r any) (any, error) {
				return call(srv.(PeerServer), ctx, r.(*Req))
			})
		},
	}
}

// ServiceDesc é o descritor do serviço, escrito à mão (N-03).
var ServiceDesc = grpc.ServiceDesc{
	ServiceName: ServiceName,
	HandlerType: (*PeerServer)(nil),
	Methods: []grpc.MethodDesc{
		handler("Step", PeerServer.Step),
		handler("Forward", PeerServer.Forward),
		handler("ReadBarrier", PeerServer.ReadBarrier),
		handler("GateUpdate", PeerServer.GateUpdate),
	},
	Metadata: "peerv1",
}

// RegisterPeerServer registra a implementação no servidor gRPC.
func RegisterPeerServer(s *grpc.Server, impl PeerServer) { s.RegisterService(&ServiceDesc, impl) }

// ServerOptions são as opções obrigatórias do servidor (codec JSON).
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{grpc.ForceServerCodec(Codec{})}
}

// DialOptions são as opções obrigatórias do cliente (codec JSON).
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.ForceCodec(Codec{}))}
}

// Client chama os quatro métodos numa conexão.
type Client struct{ cc grpc.ClientConnInterface }

func NewClient(cc grpc.ClientConnInterface) Client { return Client{cc: cc} }

func (c Client) Step(ctx context.Context, req *StepRequest) (*Empty, error) {
	out := new(Empty)
	return out, c.cc.Invoke(ctx, "/"+ServiceName+"/Step", req, out)
}

func (c Client) Forward(ctx context.Context, req *ForwardRequest) (*ForwardResponse, error) {
	out := new(ForwardResponse)
	return out, c.cc.Invoke(ctx, "/"+ServiceName+"/Forward", req, out)
}

func (c Client) ReadBarrier(ctx context.Context, req *BarrierRequest) (*BarrierResponse, error) {
	out := new(BarrierResponse)
	return out, c.cc.Invoke(ctx, "/"+ServiceName+"/ReadBarrier", req, out)
}

func (c Client) GateUpdate(ctx context.Context, req *GateUpdateRequest) (*Empty, error) {
	out := new(Empty)
	return out, c.cc.Invoke(ctx, "/"+ServiceName+"/GateUpdate", req, out)
}

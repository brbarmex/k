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

// GateUpdateRequest é o gate publicado pela líder (G-06) e o piso de retenção do disco
// calculado por ela (RT-15).
//
// To é o nó de destino (DC-19): o servidor descarta um pedido para outro nó; 0 = célula de
// versão anterior, aceito.
type GateUpdateRequest struct {
	Gate  uint64
	Floor uint64
	To    uint64 `json:",omitempty"`
}

// GateUpdateResponse leva o aplicado da follower (DC-15); nil = célula de versão anterior.
type GateUpdateResponse struct {
	Applied *uint64
}

// RetainedRequest pergunta o que a célula retém e em que estado está (RT-19).
type RetainedRequest struct{}

// RetainedResponse: ClusterID e Epoch servem à recusa da DC-11; EmptyVolume, BootToken e
// Genesis, ao fail-fast do volume perdido (DC-12). Na pré-partida, State é "partida".
type RetainedResponse struct {
	Code          string
	Node          uint64
	FirstRetained uint64
	Commit        uint64
	Gate          uint64
	State         string
	Leader        bool
	ClusterID     string
	Epoch         uint64
	EmptyVolume   bool
	BootToken     string
	Genesis       map[uint64]string
}

// CatchUpRequest pede as entradas de From a To; Owner é o nó que pede (o dono dos pinos).
type CatchUpRequest struct {
	From, To uint64
	Owner    uint64
	Node     uint64 // nó de destino (DC-19)
}

// CatchUpBatch é um lote do fluxo do CatchUp, com no máximo 1 MiB de entradas antes do
// JSON. SourceGate é o gate da fonte que cobre o lote (SG-21).
type CatchUpBatch struct {
	Code       string
	Entries    []domain.Entry
	SourceGate uint64
}

// CopyTableRequest pede a cópia das tabelas com a versão de cada item (AP-18).
type CopyTableRequest struct {
	Tables []domain.LogicalTable
	Owner  uint64
	Node   uint64 // nó de destino (DC-19)
}

// CopyTablePage é uma página do fluxo do CopyTable. F e KeySchema vêm na primeira página
// de cada tabela.
type CopyTablePage struct {
	Code      string
	F         uint64
	Table     domain.LogicalTable
	KeySchema domain.KeySchema
	Items     []VersionedItem
	TableDone bool
	Done      bool
}

// VersionedItem é um item com a chave canônica e a versão _dr_v.
type VersionedItem struct {
	Key, Item []byte
	Version   uint64
}

// CatchUpServer é o lado do servidor do fluxo do CatchUp.
type CatchUpServer interface {
	Send(*CatchUpBatch) error
	Context() context.Context
}

// CopyTableServer é o lado do servidor do fluxo do CopyTable.
type CopyTableServer interface {
	Send(*CopyTablePage) error
	Context() context.Context
}

// PeerServer é o servidor entre células.
type PeerServer interface {
	Step(ctx context.Context, req *StepRequest) (*Empty, error)
	Forward(ctx context.Context, req *ForwardRequest) (*ForwardResponse, error)
	ReadBarrier(ctx context.Context, req *BarrierRequest) (*BarrierResponse, error)
	GateUpdate(ctx context.Context, req *GateUpdateRequest) (*GateUpdateResponse, error)
	Retained(ctx context.Context, req *RetainedRequest) (*RetainedResponse, error)
	CatchUp(req *CatchUpRequest, srv CatchUpServer) error
	CopyTable(req *CopyTableRequest, srv CopyTableServer) error
}

// serverStream adapta o grpc.ServerStream a um Send tipado.
type serverStream[T any] struct{ grpc.ServerStream }

func (s serverStream[T]) Send(m *T) error { return s.SendMsg(m) }

func streamHandler[Req any, Srv any](name string, wrap func(grpc.ServerStream) Srv, call func(PeerServer, *Req, Srv) error) grpc.StreamDesc {
	return grpc.StreamDesc{
		StreamName:    name,
		ServerStreams: true,
		Handler: func(srv any, stream grpc.ServerStream) error {
			req := new(Req)
			if err := stream.RecvMsg(req); err != nil {
				return err
			}
			return call(srv.(PeerServer), req, wrap(stream))
		},
	}
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
		handler("Retained", PeerServer.Retained),
	},
	Streams: []grpc.StreamDesc{
		streamHandler("CatchUp", func(s grpc.ServerStream) CatchUpServer { return serverStream[CatchUpBatch]{s} },
			PeerServer.CatchUp),
		streamHandler("CopyTable", func(s grpc.ServerStream) CopyTableServer { return serverStream[CopyTablePage]{s} },
			PeerServer.CopyTable),
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

// Client chama os métodos numa conexão.
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

func (c Client) GateUpdate(ctx context.Context, req *GateUpdateRequest) (*GateUpdateResponse, error) {
	out := new(GateUpdateResponse)
	return out, c.cc.Invoke(ctx, "/"+ServiceName+"/GateUpdate", req, out)
}

func (c Client) Retained(ctx context.Context, req *RetainedRequest) (*RetainedResponse, error) {
	out := new(RetainedResponse)
	return out, c.cc.Invoke(ctx, "/"+ServiceName+"/Retained", req, out)
}

// ClientStream é o lado do cliente de um fluxo do servidor.
type ClientStream[T any] struct{ s grpc.ClientStream }

// Recv devolve a próxima mensagem; io.EOF no fim do fluxo.
func (c ClientStream[T]) Recv() (*T, error) {
	m := new(T)
	if err := c.s.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func openStream[T any](ctx context.Context, cc grpc.ClientConnInterface, i int, req any) (ClientStream[T], error) {
	desc := &ServiceDesc.Streams[i]
	s, err := cc.NewStream(ctx, desc, "/"+ServiceName+"/"+desc.StreamName)
	if err != nil {
		return ClientStream[T]{}, err
	}
	if err := s.SendMsg(req); err != nil {
		return ClientStream[T]{}, err
	}
	if err := s.CloseSend(); err != nil {
		return ClientStream[T]{}, err
	}
	return ClientStream[T]{s: s}, nil
}

// CatchUp abre o fluxo das entradas (RT-19).
func (c Client) CatchUp(ctx context.Context, req *CatchUpRequest) (ClientStream[CatchUpBatch], error) {
	return openStream[CatchUpBatch](ctx, c.cc, 0, req)
}

// CopyTable abre o fluxo da cópia das tabelas (AP-18).
func (c Client) CopyTable(ctx context.Context, req *CopyTableRequest) (ClientStream[CopyTablePage], error) {
	return openStream[CopyTablePage](ctx, c.cc, 1, req)
}

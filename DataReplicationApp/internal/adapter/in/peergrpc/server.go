// Package peergrpc é o servidor gRPC entre células (RT-12): recebe mensagens do Raft,
// escritas encaminhadas, pedidos de barreira de leitura e o gate publicado pela líder, e
// serve o canal de recuperação (RT-19): Retained, CatchUp e CopyTable. Só traduz protocolo
// e respeita os pinos e o gate da fonte.
package peergrpc

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"

	"example.com/datareplication/api/peerv1"
	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// LeaderBarrier é a barreira de leitura servida pela líder (usecase.ReadService).
type LeaderBarrier interface {
	LeaderBarrier(ctx context.Context) (readIndex, gate domain.Index, err error)
}

// GateReceiver recebe o gate publicado pela líder e devolve o aplicado local
// (usecase.GateSync, DC-15).
type GateReceiver interface {
	OnGateUpdate(gate, floor domain.Index) (applied domain.Index, ok bool)
}

// PreBoot é a resposta da pré-partida (DC-12): a célula ainda não abriu o WAL e só diz
// que está vazia e qual é o seu token de partida.
type PreBoot struct {
	ClusterID string
	BootToken string
}

// wrongNode diz se o pedido é para outro nó (DC-19): a conexão de um par pode cair noutra
// célula quando o IP de um container reiniciado é reaproveitado. 0 = sem destino (versão
// anterior), aceito.
func (s *Server) wrongNode(to uint64) bool { return to != 0 && s.Self != 0 && to != s.Self }

// Server implementa peerv1.PeerServer.
type Server struct {
	// Self é o ID do nó local, conferido contra o destino dos pedidos (DC-19); 0 = não confere.
	Self    uint64
	Raft    port.RaftInbound
	Writer  port.Writer
	Barrier LeaderBarrier
	Gate    GateReceiver

	// PreBoot liga o modo pré-partida (DC-12): Retained responde só a identidade e o token,
	// o Step é descartado e o resto responde SOURCE_NOT_READY.
	PreBoot *PreBoot

	// Canal de recuperação (RT-19).
	Log     port.LogSource
	Pins    port.RetentionPins
	Scanner port.Scanner
	Catalog port.Catalog
	Mode    *domain.CellMode
	// SourceGate é o gate da célula (o campo Gate já é o receptor do GateUpdate): o CatchUp
	// nunca envia além dele (SG-21).
	SourceGate *domain.Gate
	Applied    port.AppliedWaiter
	Status     port.RaftStatus
	Tables     []domain.LogicalTable
	PinTTL     time.Duration // REBUILD_PIN_TTL (DC-10)
	// Identity é o cluster_id e o epoch do marcador local (DC-11); Genesis, os tokens
	// gravados no metadado do WAL (DC-12).
	Identity func() (clusterID string, epoch uint64)
	Genesis  func() map[uint64]string

	once sync.Once
	srv  *grpc.Server
}

// Limites do canal de recuperação: cada mensagem do fluxo leva no máximo 1 MiB de
// entradas ou 100 itens antes do JSON, abaixo dos 4 MiB do gRPC.
const (
	catchUpBytes = 1 << 20
	copyPage     = 100
)

func (s *Server) grpcServer() *grpc.Server {
	s.once.Do(func() {
		s.srv = grpc.NewServer(peerv1.ServerOptions()...)
		peerv1.RegisterPeerServer(s.srv, s)
	})
	return s.srv
}

var _ peerv1.PeerServer = (*Server)(nil)

func (s *Server) Step(ctx context.Context, req *peerv1.StepRequest) (*peerv1.Empty, error) {
	if s.PreBoot != nil {
		return &peerv1.Empty{}, nil // sem WAL ainda: nada chega ao Raft (DC-12)
	}
	return &peerv1.Empty{}, s.Raft.Step(ctx, req.Msgs)
}

// Forward executa na líder o mesmo caminho da escrita local; a requisição vem marcada
// como encaminhada e nunca é reencaminhada (um salto, ADR-010).
func (s *Server) Forward(ctx context.Context, req *peerv1.ForwardRequest) (*peerv1.ForwardResponse, error) {
	if s.PreBoot != nil {
		return &peerv1.ForwardResponse{Code: peerv1.CodeNotLeader}, nil
	}
	res, err := s.Writer.Write(ctx, port.WriteRequest{Ops: req.Ops, Forwarded: true})
	return &peerv1.ForwardResponse{Code: peerv1.CodeOf(err), Index: uint64(res.Index)}, nil
}

func (s *Server) ReadBarrier(ctx context.Context, _ *peerv1.BarrierRequest) (*peerv1.BarrierResponse, error) {
	if s.PreBoot != nil {
		return &peerv1.BarrierResponse{Code: peerv1.CodeNotLeader}, nil
	}
	ri, gate, err := s.Barrier.LeaderBarrier(ctx)
	return &peerv1.BarrierResponse{Code: peerv1.CodeOf(err), ReadIndex: uint64(ri), Gate: uint64(gate)}, nil
}

func (s *Server) GateUpdate(_ context.Context, req *peerv1.GateUpdateRequest) (*peerv1.GateUpdateResponse, error) {
	resp := &peerv1.GateUpdateResponse{}
	if s.PreBoot != nil || s.wrongNode(req.To) {
		return resp, nil
	}
	if applied, ok := s.Gate.OnGateUpdate(domain.Index(req.Gate), domain.Index(req.Floor)); ok {
		v := uint64(applied)
		resp.Applied = &v
	}
	return resp, nil
}

// Retained diz o que a célula retém e em que estado está (RT-19). Na pré-partida, só a
// identidade, o volume vazio e o token (DC-12).
func (s *Server) Retained(_ context.Context, _ *peerv1.RetainedRequest) (*peerv1.RetainedResponse, error) {
	if pb := s.PreBoot; pb != nil {
		return &peerv1.RetainedResponse{Code: peerv1.CodeOK, Node: s.Self, ClusterID: pb.ClusterID, EmptyVolume: true,
			BootToken: pb.BootToken, State: "partida"}, nil
	}
	st := s.Status.Status()
	resp := &peerv1.RetainedResponse{Code: peerv1.CodeOK, Node: st.NodeID, FirstRetained: uint64(s.Log.FirstRetained()),
		Commit: uint64(st.Commit), Gate: uint64(s.SourceGate.Index()), State: s.Mode.State().String(),
		Leader: st.Lead != 0 && st.Lead == st.NodeID}
	if s.Identity != nil {
		resp.ClusterID, resp.Epoch = s.Identity()
	}
	if s.Genesis != nil {
		resp.Genesis = s.Genesis()
	}
	return resp, nil
}

func owner(prefix string, node uint64) string { return prefix + strconv.FormatUint(node, 10) }

// CatchUp envia as entradas de From a To, nunca além do gate da fonte (SG-21). O começo do
// CatchUp de uma célula reconstruída solta o pino da reconstrução dela (DC-10).
func (s *Server) CatchUp(req *peerv1.CatchUpRequest, stream peerv1.CatchUpServer) error {
	code := func(c string) error { return stream.Send(&peerv1.CatchUpBatch{Code: c}) }
	if s.PreBoot != nil || s.wrongNode(req.Node) || s.Mode.State() != domain.CellNormal {
		return code(peerv1.CodeSourceNotReady)
	}
	from, to := domain.Index(req.From), domain.Index(req.To)
	if from == 0 || from > to {
		return code(peerv1.CodeInternal)
	}
	if from < s.Log.FirstRetained() {
		return code(peerv1.CodeNotRetained)
	}
	s.Pins.Unpin(owner("reconstrucao-", req.Owner))
	pinOwner := owner("catchup-", req.Owner)
	release := s.Pins.Pin(pinOwner, from-1)
	defer release()
	ctx := stream.Context()
	for next := from; next <= to; {
		changed := s.SourceGate.Changed()
		hi := min(to, s.SourceGate.Index())
		if next > hi {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				continue
			}
		}
		ents, err := s.Log.Read(ctx, next, hi, catchUpBytes)
		if errors.Is(err, port.ErrNotRetained) {
			return code(peerv1.CodeNotRetained)
		}
		if err != nil {
			return err
		}
		if len(ents) == 0 {
			// O commit local ainda não chegou ao gate recebido (follower): espera o log.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}
		last := ents[len(ents)-1].Index
		if err := stream.Send(&peerv1.CatchUpBatch{Code: peerv1.CodeOK, Entries: ents, SourceGate: uint64(hi)}); err != nil {
			return err
		}
		next = last + 1
		s.Pins.PinFor(pinOwner, last, 0) // o pino acompanha o que já foi enviado
	}
	return nil
}

// CopyTable envia as tabelas com a versão de cada item (AP-18). F é o aplicado da fonte no
// começo da cópia; o pino em F − 1 segura o log que a célula reconstruída vai pedir depois
// pelo CatchUp, até ele começar ou o REBUILD_PIN_TTL passar (DC-10).
func (s *Server) CopyTable(req *peerv1.CopyTableRequest, stream peerv1.CopyTableServer) error {
	code := func(c string) error { return stream.Send(&peerv1.CopyTablePage{Code: c}) }
	if s.PreBoot != nil || s.wrongNode(req.Node) || s.Mode.State() != domain.CellNormal {
		return code(peerv1.CodeSourceNotReady)
	}
	f := s.Applied.Applied()
	pinOwner := owner("reconstrucao-", req.Owner)
	pinAt := domain.Index(0)
	if f > 0 {
		pinAt = f - 1
	}
	s.Pins.PinFor(pinOwner, pinAt, 0) // sem expiração durante o fluxo
	aborted := false
	defer func() {
		if !aborted {
			s.Pins.PinFor(pinOwner, pinAt, s.PinTTL)
		}
	}()
	ctx := stream.Context()
	for _, t := range req.Tables {
		ks, ok := s.Catalog.KeySchema(t)
		if !ok {
			return code(peerv1.CodeInternal)
		}
		var start []byte
		first := true
		for {
			if !s.Pins.Has(pinOwner) { // o teto duro soltou o pino (RT-15)
				aborted = true
				return code(peerv1.CodeRebuildAborted)
			}
			page, err := s.Scanner.ScanVersioned(ctx, t, copyPage, start)
			if err != nil {
				return err
			}
			out := &peerv1.CopyTablePage{Code: peerv1.CodeOK, Table: t, Items: make([]peerv1.VersionedItem, len(page.Items))}
			if first {
				out.F, out.KeySchema, first = uint64(f), ks, false
			}
			for i, it := range page.Items {
				out.Items[i] = peerv1.VersionedItem{Key: it.Key, Item: it.Item, Version: uint64(it.Version)}
			}
			out.TableDone = page.LastKey == nil
			if err := stream.Send(out); err != nil {
				return err
			}
			if out.TableDone {
				break
			}
			start = page.LastKey
		}
	}
	return stream.Send(&peerv1.CopyTablePage{Code: peerv1.CodeOK, F: uint64(f), Done: true})
}

// Serve abre o servidor gRPC no listener (sem TLS na POC).
func (s *Server) Serve(lis net.Listener) error {
	return s.grpcServer().Serve(lis)
}

// Stop encerra o servidor, esperando as chamadas em curso.
func (s *Server) Stop() {
	s.grpcServer().GracefulStop()
}

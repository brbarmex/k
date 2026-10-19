// Package etcdraft é o motor de consenso real (RT-01 a RT-20): go.etcd.io/raft/v3 e o
// wal do etcd, um WAL por shard. É o ÚNICO pacote que importa essas bibliotecas; o
// resto do código troca mensagens do Raft como bytes.
package etcdraft

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
)

// ErrMembership: os votantes da configuração diferem da ConfState gravada no WAL (RT-10).
// A POC não troca membros: a célula não sobe.
var ErrMembership = errors.New("PEERS difere do que está gravado no WAL")

// ErrIdentity: o metadado do WAL é de outro nó, de outro grupo ou do formato antigo (RT-20).
var ErrIdentity = errors.New("identidade do WAL não confere")

// bootIndex e bootTerm são o snapshot sintético de partida (RT-03): todas as células
// nascem dele, com a mesma ConfState. A primeira entrada útil tem índice 2.
const (
	bootIndex = 1
	bootTerm  = 1
)

// walMeta é o metadado gravado no WAL (RT-20). Genesis são os tokens de partida vistos no
// nascimento do grupo (DC-12, Incremento B); não entra na comparação de identidade.
type walMeta struct {
	NodeID    uint64            `json:"node_id"`
	ClusterID string            `json:"cluster_id"`
	Genesis   map[uint64]string `json:"genesis,omitempty"`
}

// opened é o resultado da abertura do WAL na partida.
type opened struct {
	w       *wal.WAL
	ms      *raft.MemoryStorage
	snap    domain.Index      // índice do último snapshot do WAL
	cs      *raftpb.ConfState // membros gravados
	marker  domain.SnapshotMarker
	ents    []*raftpb.Entry // a cauda relida (depois do snapshot)
	genesis map[uint64]string
}

// segMu protege wal.SegmentSizeBytes, uma variável global do etcd (DC-07): é ajustada uma
// vez por processo, antes do primeiro Create/Open.
var (
	segMu  sync.Mutex
	segSet int64
)

// setSegmentSize ajusta o tamanho do segmento do WAL. 0 não altera o valor atual (o
// padrão do etcd ou o já definido); um valor diferente do já definido é erro.
func setSegmentSize(n int64) error {
	if n == 0 {
		return nil
	}
	segMu.Lock()
	defer segMu.Unlock()
	switch {
	case segSet == n:
		return nil
	case segSet != 0:
		return fmt.Errorf("etcdraft: tamanho do segmento do WAL já definido como %d neste processo; pedido %d", segSet, n)
	}
	wal.SegmentSizeBytes, segSet = n, n
	return nil
}

// openStorage abre (ou cria) o WAL do shard e devolve o MemoryStorage já carregado.
// Partida do zero: grava o snapshot sintético e o HardState inicial. Reinício: confere a
// identidade (RT-20) e a ConfState gravada com a configuração (RT-10) e relê só a cauda
// depois do último snapshot (RT-09).
func openStorage(cfg Config) (*opened, error) {
	if err := setSegmentSize(cfg.SegmentBytes); err != nil {
		return nil, err
	}
	lg := zap.NewNop()
	cs := &raftpb.ConfState{Voters: sorted(cfg.Voters)}
	ms := raft.NewMemoryStorage()
	marker, _, err := readMarker(cfg.MarkerPath, cfg.ClusterID)
	if err != nil {
		return nil, err
	}
	if marker.ClusterID != cfg.ClusterID {
		return nil, fmt.Errorf("%w: marcador do grupo %q, configuração %q; rode task clean", ErrIdentity, marker.ClusterID, cfg.ClusterID)
	}

	if !wal.Exist(cfg.Dir) {
		meta, _ := json.Marshal(walMeta{NodeID: cfg.ID, ClusterID: cfg.ClusterID, Genesis: cfg.Genesis})
		w, err := wal.Create(lg, cfg.Dir, meta)
		if err != nil {
			return nil, fmt.Errorf("etcdraft: criar o WAL: %w", err)
		}
		snap := &walpb.Snapshot{Index: proto.Uint64(bootIndex), Term: proto.Uint64(bootTerm), ConfState: cs}
		hs := &raftpb.HardState{Term: proto.Uint64(bootTerm), Commit: proto.Uint64(bootIndex)}
		if err := w.SaveSnapshot(snap); err != nil {
			_ = w.Close()
			return nil, fmt.Errorf("etcdraft: gravar o snapshot sintético: %w", err)
		}
		if err := w.Save(hs, nil); err != nil {
			_ = w.Close()
			return nil, fmt.Errorf("etcdraft: gravar o HardState inicial: %w", err)
		}
		if err := ms.ApplySnapshot(raftSnapshot(bootIndex, bootTerm, cs, marker)); err != nil {
			_ = w.Close()
			return nil, err
		}
		_ = ms.SetHardState(hs)
		return &opened{w: w, ms: ms, snap: bootIndex, cs: cs, marker: marker, genesis: cfg.Genesis}, nil
	}

	snaps, err := wal.ValidSnapshotEntries(lg, cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("etcdraft: ler os snapshots do WAL: %w", err)
	}
	if len(snaps) == 0 {
		return nil, fmt.Errorf("etcdraft: WAL sem snapshot")
	}
	last := snaps[len(snaps)-1]
	got := last.GetConfState()
	if !slices.Equal(sorted(got.GetVoters()), cs.Voters) || len(got.GetLearners()) != 0 {
		return nil, fmt.Errorf("%w (gravado: votantes %v; configuração: votantes %v); rode task limpar",
			ErrMembership, got.GetVoters(), cs.Voters)
	}
	w, err := wal.Open(lg, cfg.Dir, last)
	if err != nil {
		return nil, fmt.Errorf("etcdraft: abrir o WAL: %w", err)
	}
	metaB, hs, ents, err := w.ReadAll()
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("etcdraft: ler o WAL: %w", err)
	}
	var meta walMeta
	if err := json.Unmarshal(metaB, &meta); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("%w: WAL de formato antigo; rode task clean", ErrIdentity)
	}
	if meta.NodeID != cfg.ID || meta.ClusterID != cfg.ClusterID {
		_ = w.Close()
		return nil, fmt.Errorf("%w: gravado nó %d do grupo %q, configurado nó %d do grupo %q; rode task clean",
			ErrIdentity, meta.NodeID, meta.ClusterID, cfg.ID, cfg.ClusterID)
	}
	if err := ms.ApplySnapshot(raftSnapshot(last.GetIndex(), last.GetTerm(), got, marker)); err != nil {
		_ = w.Close()
		return nil, err
	}
	if hs != nil {
		_ = ms.SetHardState(hs)
	}
	if err := ms.Append(ents); err != nil {
		_ = w.Close()
		return nil, err
	}
	return &opened{w: w, ms: ms, snap: domain.Index(last.GetIndex()), cs: got, marker: marker, ents: ents, genesis: meta.Genesis}, nil
}

// raftSnapshot é o snapshot do MemoryStorage: índice, termo e membros do WAL, e o
// marcador no Data (DC-06). É o que a líder envia no MsgSnap (RT-17).
func raftSnapshot(idx, term uint64, cs *raftpb.ConfState, m domain.SnapshotMarker) *raftpb.Snapshot {
	return &raftpb.Snapshot{Data: m.Encode(), Metadata: &raftpb.SnapshotMetadata{
		Index: proto.Uint64(idx), Term: proto.Uint64(term), ConfState: cs,
	}}
}

func sorted(ids []uint64) []uint64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	if out == nil {
		out = []uint64{}
	}
	return out
}

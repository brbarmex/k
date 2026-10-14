// Package etcdraft é o motor de consenso real (RT-01 a RT-11): go.etcd.io/raft/v3 e o
// wal do etcd, um WAL por shard. É o ÚNICO pacote que importa essas bibliotecas; o
// resto do código troca mensagens do Raft como bytes.
package etcdraft

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// ErrMembership: os votantes ou learners da configuração diferem da ConfState gravada no
// WAL (RT-10). A POC não troca membros: a célula não sobe.
var ErrMembership = errors.New("PEERS/LEARNERS diferem do que está gravado no WAL")

// bootIndex e bootTerm são o snapshot sintético de partida (RT-03): todas as células
// nascem dele, com a mesma ConfState. A primeira entrada útil tem índice 2.
const (
	bootIndex = 1
	bootTerm  = 1
)

// openStorage abre (ou cria) o WAL do shard e devolve o MemoryStorage já carregado.
// Partida do zero: grava o snapshot sintético e o HardState inicial. Reinício: confere a
// ConfState gravada com a configuração e recarrega HardState e entradas (RT-09, RT-10).
func openStorage(cfg Config) (*wal.WAL, *raft.MemoryStorage, error) {
	lg := zap.NewNop()
	cs := &raftpb.ConfState{Voters: sorted(cfg.Voters), Learners: sorted(cfg.Learners)}
	ms := raft.NewMemoryStorage()

	if !wal.Exist(cfg.Dir) {
		w, err := wal.Create(lg, cfg.Dir, []byte(strconv.FormatUint(cfg.ID, 10)))
		if err != nil {
			return nil, nil, fmt.Errorf("etcdraft: criar o WAL: %w", err)
		}
		snap := &walpb.Snapshot{Index: proto.Uint64(bootIndex), Term: proto.Uint64(bootTerm), ConfState: cs}
		hs := &raftpb.HardState{Term: proto.Uint64(bootTerm), Commit: proto.Uint64(bootIndex)}
		if err := w.SaveSnapshot(snap); err != nil {
			_ = w.Close()
			return nil, nil, fmt.Errorf("etcdraft: gravar o snapshot sintético: %w", err)
		}
		if err := w.Save(hs, nil); err != nil {
			_ = w.Close()
			return nil, nil, fmt.Errorf("etcdraft: gravar o HardState inicial: %w", err)
		}
		if err := ms.ApplySnapshot(bootSnapshot(cs)); err != nil {
			_ = w.Close()
			return nil, nil, err
		}
		_ = ms.SetHardState(hs)
		return w, ms, nil
	}

	snaps, err := wal.ValidSnapshotEntries(lg, cfg.Dir)
	if err != nil {
		return nil, nil, fmt.Errorf("etcdraft: ler os snapshots do WAL: %w", err)
	}
	if len(snaps) == 0 {
		return nil, nil, fmt.Errorf("etcdraft: WAL sem snapshot")
	}
	last := snaps[len(snaps)-1]
	got := last.GetConfState()
	if !slices.Equal(sorted(got.GetVoters()), cs.Voters) || !slices.Equal(sorted(got.GetLearners()), cs.Learners) {
		return nil, nil, fmt.Errorf("%w (gravado: votantes %v, learners %v; configuração: votantes %v, learners %v); rode task limpar",
			ErrMembership, got.GetVoters(), got.GetLearners(), cs.Voters, cs.Learners)
	}
	w, err := wal.Open(lg, cfg.Dir, last)
	if err != nil {
		return nil, nil, fmt.Errorf("etcdraft: abrir o WAL: %w", err)
	}
	meta, hs, ents, err := w.ReadAll()
	if err != nil {
		_ = w.Close()
		return nil, nil, fmt.Errorf("etcdraft: ler o WAL: %w", err)
	}
	if string(meta) != strconv.FormatUint(cfg.ID, 10) {
		_ = w.Close()
		return nil, nil, fmt.Errorf("etcdraft: o WAL é do nó %s, não do nó %d; rode task limpar", meta, cfg.ID)
	}
	snap := &raftpb.Snapshot{Metadata: &raftpb.SnapshotMetadata{Index: proto.Uint64(last.GetIndex()), Term: proto.Uint64(last.GetTerm()), ConfState: got}}
	if err := ms.ApplySnapshot(snap); err != nil {
		_ = w.Close()
		return nil, nil, err
	}
	if hs != nil {
		_ = ms.SetHardState(hs)
	}
	if err := ms.Append(ents); err != nil {
		_ = w.Close()
		return nil, nil, err
	}
	return w, ms, nil
}

func bootSnapshot(cs *raftpb.ConfState) *raftpb.Snapshot {
	return &raftpb.Snapshot{Metadata: &raftpb.SnapshotMetadata{
		Index: proto.Uint64(bootIndex), Term: proto.Uint64(bootTerm), ConfState: cs,
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

package etcdraft

import (
	"sync"
	"sync/atomic"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
)

// ramTracker conta as entradas e os bytes no MemoryStorage (RT-14, SG-23), ao lado de
// cada Append, Compact e ApplySnapshot. Guarda um tamanho por índice a partir de first.
type ramTracker struct {
	mu    sync.Mutex
	first domain.Index // índice do primeiro tamanho guardado
	sizes []uint32

	bytes    atomic.Int64
	appended atomic.Int64 // bytes acrescentados desde a partida (gatilho do snapshot)
}

// recordAppend registra entradas acrescentadas. Um índice ≤ o último existente trunca a
// partir dele (a troca de líder reescreve entradas não commitadas), como o MemoryStorage.
func (r *ramTracker) recordAppend(ents []*raftpb.Entry) {
	if len(ents) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range ents {
		idx := domain.Index(e.GetIndex())
		if idx < r.first {
			continue // já compactado: o MemoryStorage também ignora
		}
		if pos := int(idx - r.first); pos < len(r.sizes) {
			var cut int64
			for _, s := range r.sizes[pos:] {
				cut += int64(s)
			}
			r.sizes = r.sizes[:pos]
			r.bytes.Add(-cut)
		}
		sz := uint32(proto.Size(e))
		r.sizes = append(r.sizes, sz)
		r.bytes.Add(int64(sz))
		r.appended.Add(int64(sz))
	}
}

// recordCompact descarta os tamanhos até to, inclusive.
func (r *ramTracker) recordCompact(to domain.Index) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if to < r.first {
		return
	}
	n := min(int(to-r.first+1), len(r.sizes))
	var cut int64
	for _, s := range r.sizes[:n] {
		cut += int64(s)
	}
	r.sizes = append([]uint32(nil), r.sizes[n:]...)
	r.first = to + 1
	r.bytes.Add(-cut)
}

// recordSnapshot zera a contagem: o MemoryStorage passa a começar depois de s.
func (r *ramTracker) recordSnapshot(s domain.Index) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var cut int64
	for _, sz := range r.sizes {
		cut += int64(sz)
	}
	r.sizes, r.first = nil, s+1
	r.bytes.Add(-cut)
}

// stats devolve as entradas e os bytes no MemoryStorage.
func (r *ramTracker) stats() (uint64, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return uint64(len(r.sizes)), r.bytes.Load()
}

package port

import (
	"context"

	"example.com/datareplication/internal/domain"
)

// ---- Portas de saída (driven): o núcleo pede, o adaptador implementa ----

// ReplicationLog é o log de replicação próprio (5.5.1). Append só retorna após fsync.
type ReplicationLog interface {
	Append(ctx context.Context, batch []domain.Entry) error
	Read(ctx context.Context, from, to domain.Index) ([]domain.Entry, error)
	TruncateSuffix(ctx context.Context, from domain.Index) error
	CompactPrefix(ctx context.Context, upTo domain.Index) error
	DurableIndex() domain.Index
}

// ConsensusEngine esconde a biblioteca Raft (5.5).
type ConsensusEngine interface {
	Propose(ctx context.Context, e domain.Entry) (domain.Index, error)
	ReadIndex(ctx context.Context) (domain.Index, error)
	Leader() (LeaderInfo, bool)
	TransferLeadership(ctx context.Context, to string) error
	Committed() <-chan CommitBatch // entradas já commitadas, entregues da memória
	LearnerDurable() [3]domain.Index
}

// CommitBatch é um lote de entradas commitadas pelo Raft, em ordem.
type CommitBatch struct {
	Entries []domain.Entry
}

type LeaderInfo struct {
	NodeID string
	Local  bool
}

// StateStore é o banco da célula (DynamoDB ou Cosmos via Cloud-Adapter).
type StateStore interface {
	ApplyBatch(ctx context.Context, ops []VersionedOp) error
	Get(ctx context.Context, table domain.LogicalTable, key []byte, consistent bool) ([]byte, error)
	AppliedIndex() domain.Index
}

type VersionedOp struct {
	domain.Op
	Version domain.Index
}

// Forwarder leva a escrita inteira à líder quando a célula é follower (ADR-010).
// A líder executa o mesmo caminho (propor, gate, apply local) e devolve o resultado.
type Forwarder interface {
	Forward(ctx context.Context, leader string, r WriteRequest) (WriteResult, error)
}

// AppliedWaiter bloqueia até o Applier da célula ter aplicado o índice no banco.
// O ACK de uma escrita só sai depois disso na célula da líder (6.9, item 4).
type AppliedWaiter interface {
	WaitApplied(ctx context.Context, idx domain.Index) error
}

// ---- Portas de entrada (driving): o adaptador chama, o núcleo implementa ----

type WriteRequest struct {
	Ops      []domain.Op
	ClientID string // id estável do SDK, se houver
	Deadline int64  // unix nano
}

type WriteResult struct {
	Index    domain.Index
	Degraded bool // vira o header x-dr-mode: degraded (D10)
}

type Writer interface {
	Write(ctx context.Context, r WriteRequest) (WriteResult, error)
}

type Reader interface {
	Get(ctx context.Context, table domain.LogicalTable, key []byte, strong bool) ([]byte, error)
}

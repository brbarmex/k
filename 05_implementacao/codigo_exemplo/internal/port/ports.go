package port

import (
	"context"
	"time"

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
	// Term é o termo atual da líder local (ADR-046: o gate só adota progresso desse termo).
	Term() domain.Term
	// LearnerEvents entrega CADA resposta de AppendEntries dos learners da Azure, no
	// momento em que chega (o gate é alimentado por evento, ADR-046). O progresso da
	// Azure não tem outro canal.
	LearnerEvents() <-chan LearnerProgress
	// LearnerDurable é o retrato atual do progresso: (termo, índice, identidade) por learner.
	LearnerDurable() []LearnerProgress
}

// LearnerProgress é o índice de casamento devolvido por um learner depois do fsync,
// com o termo em que foi confirmado e a identidade do learner (ADR-046, ADR-034).
type LearnerProgress struct {
	Term    domain.Term
	Index   domain.Index
	Learner string
	// SentAt é o instante de ENVIO (relógio monotônico da líder) da mensagem que o
	// learner confirmou, heartbeat ou AppendEntries. Alimenta t_c, o último contato com a
	// maioria Azure, de onde o lease do modo degradado conta (ADR-044).
	SentAt time.Time
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
	// Forwarded marca a requisição que já foi encaminhada por um follower: nunca é
	// reencaminhada (limite de um salto, ADR-039).
	Forwarded bool
}

type WriteResult struct {
	Index    domain.Index
	Degraded bool // vira o header x-dr-mode: degraded (D10); vem do estado real do shard
}

type Writer interface {
	Write(ctx context.Context, r WriteRequest) (WriteResult, error)
}

type Reader interface {
	Get(ctx context.Context, table domain.LogicalTable, key []byte, strong bool) ([]byte, error)
}

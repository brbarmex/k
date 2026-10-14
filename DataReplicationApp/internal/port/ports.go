package port

import (
	"context"
	"errors"
	"time"

	"example.com/datareplication/internal/domain"
)

// ---- Portas de saída (driven): o núcleo pede, o adaptador implementa ----

// ReplicationLog é o log de replicação (5.5.1): log segmentado próprio, em volume separado,
// derivado do log do Raft (ADR-055). Append grava SEM fsync (fora do caminho do ACK).
// Sync é o checkpoint durável: faz fsync e devolve o novo DurableIndex. Só até o
// DurableIndex o log do Raft pode ser compactado; nunca até o índice apenas copiado.
type ReplicationLog interface {
	Append(ctx context.Context, batch []domain.Entry) error
	Read(ctx context.Context, from, to domain.Index) ([]domain.Entry, error)
	TruncateSuffix(ctx context.Context, from domain.Index) error
	CompactPrefix(ctx context.Context, upTo domain.Index) error
	Sync(ctx context.Context) (domain.Index, error)
	DurableIndex() domain.Index
	LastIndex() domain.Index
}

// ApplyProgress guarda o progresso do applier por item (índice, ordem), no volume do log
// de replicação e sem fsync (ADR-048, ADR-055). Perder o progresso só alonga o replay,
// que é idempotente pela versão.
type ApplyProgress interface {
	SaveProgress(idx domain.Index, ord uint32) error
	LoadProgress() (domain.Index, uint32, error)
}

// RaftCompactor é o que o checkpoint precisa do adaptador do Raft para compactar o log
// do Raft (ADR-055): o menor índice confirmado pelos pares e a compactação em si.
type RaftCompactor interface {
	MinPeerMatch() domain.Index
	Compact(upTo domain.Index) error
}

// ConsensusEngine esconde a biblioteca Raft (5.5).
type ConsensusEngine interface {
	Propose(ctx context.Context, e domain.Entry) (domain.Index, error)
	ReadIndex(ctx context.Context) (domain.Index, error)
	Leader() (LeaderInfo, bool)
	TransferLeadership(ctx context.Context, to string) error
	// Propose devolve usecase.ErrBackpressure (com a causa) quando o Raft descarta a
	// proposta (ADR-056). Nenhuma resposta do Raft sai antes do fsync do lote.
	//
	// O commit é um SINAL, não uma fila (ADR-060): o laço do Raft só publica o índice e
	// nunca espera quem consome. CommitIndex é o último commitado; CommitChanged devolve um
	// canal fechado no próximo avanço (pegar o canal ANTES de ler o índice);
	// CommittedEntries lê da memória as entradas commitadas de lo a hi (inclusive), até
	// maxBytes, já estáveis no disco da própria célula; FirstIndex é o primeiro índice
	// ainda guardado.
	CommitIndex() domain.Index
	CommitChanged() <-chan struct{}
	CommittedEntries(lo, hi domain.Index, maxBytes uint64) ([]domain.Entry, error)
	FirstIndex() domain.Index
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

type LeaderInfo struct {
	NodeID string
	Local  bool
}

// StateStore é o banco da célula (DynamoDB ou Cosmos via Cloud-Adapter). Toda leitura é
// consistente (ADR-057). O índice aplicado é do Applier, não do banco.
type StateStore interface {
	ApplyBatch(ctx context.Context, ops []VersionedOp) error
	// Get devolve o item em JSON do DynamoDB sem _dr_v, ou nil se não existe.
	Get(ctx context.Context, table domain.LogicalTable, key []byte) ([]byte, error)
}

// Scanner é a varredura da tabela base no banco da célula, sempre consistente.
type Scanner interface {
	Scan(ctx context.Context, table domain.LogicalTable, limit int, startKey []byte) (ScanPage, error)
}

// ScanPage é uma página de Scan: itens em JSON do DynamoDB (sem _dr_v), a chave
// canônica do último item avaliado (nil no fim) e quantos itens foram avaliados.
type ScanPage struct {
	Items   [][]byte
	LastKey []byte
	Scanned int
}

// Erros do banco da célula, classificados pelo adaptador (AP-07). O adaptador embrulha
// o erro original com %w; "já aplicado" (condição de versão) não é erro.
var (
	ErrStoreTransient = errors.New("erro transitório do banco")
	ErrStorePermanent = errors.New("erro permanente do banco")
)

// Catalog é o KeySchema de cada tabela lógica, lido do banco na partida.
type Catalog interface {
	KeySchema(t domain.LogicalTable) (domain.KeySchema, bool)
	Ready() bool
}

// BarrierClient pergunta à líder o ReadIndex e o gate na MESMA resposta (A.6, R16).
type BarrierClient interface {
	Barrier(ctx context.Context, leader string) (readIndex, gate domain.Index, err error)
}

// GatePublisher leva o gate da líder às outras células (G-06). Nunca bloqueia e nunca
// perde o valor mais recente.
type GatePublisher interface {
	Publish(term domain.Term, gate domain.Index)
}

// RaftInbound recebe mensagens do Raft já serializadas, vindas de um par.
type RaftInbound interface {
	Step(ctx context.Context, msgs [][]byte) error
}

// RaftStatus é o retrato do Raft local para o /status e o /readyz.
type RaftStatus interface {
	Status() RaftInfo
}

// RaftInfo é o estado do nó Raft local. Match e QuorumActive só são preenchidos na líder.
type RaftInfo struct {
	NodeID  uint64
	Lead    uint64
	Term    domain.Term
	Commit  domain.Index
	Running bool
	Match   map[uint64]domain.Index
	// QuorumActive: votantes ativos recentemente, contando a própria líder; 0 na follower.
	QuorumActive int
	// Voters: número de votantes da configuração.
	Voters int
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
	// Applied é a marca d'água atual (admissão por atraso do apply, ADR-060).
	Applied() domain.Index
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

// Reader é a leitura forte (A.6): sempre forte, uma barreira por chamada.
type Reader interface {
	Get(ctx context.Context, table domain.LogicalTable, key []byte) ([]byte, error)
	Scan(ctx context.Context, table domain.LogicalTable, limit int, startKey []byte) (ScanPage, error)
}

package port

import (
	"context"
	"errors"
	"math"
	"time"

	"example.com/datareplication/internal/domain"
)

// ---- Portas de saída (driven): o núcleo pede, o adaptador implementa ----

// ApplyProgress guarda o progresso do applier por item (índice, ordem), no volume do log
// do Raft e sem fsync (ADR-048). Perder o progresso só alonga o replay, que é idempotente
// pela versão.
//
// Sync torna durável o maior índice gravado antes dele e devolve o índice durável (AP-06):
// o Compactor chama Sync antes de cada snapshot e de cada liberação de disco, e o disco
// nunca é liberado além dele (SG-18, G1).
type ApplyProgress interface {
	SaveProgress(idx domain.Index, ord uint32) error
	LoadProgress() (domain.Index, uint32, error)
	Sync() (domain.Index, error)
}

// OrdComplete na ordem do progresso quer dizer que a entrada inteira já está no banco
// (usada pela reconstrução, DC-04).
const OrdComplete = math.MaxUint32

// ConsensusEngine esconde a biblioteca Raft (5.5).
type ConsensusEngine interface {
	Propose(ctx context.Context, e domain.Entry) (domain.Index, error)
	ReadIndex(ctx context.Context) (domain.Index, error)
	Leader() (LeaderInfo, bool)
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
	// Term é o termo atual visto pelo nó local.
	Term() domain.Term
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
// ScanVersioned é a mesma varredura com a versão _dr_v de cada item, para a cópia da
// reconstrução (AP-18, DC-11); nunca chega à API.
type Scanner interface {
	Scan(ctx context.Context, table domain.LogicalTable, limit int, startKey []byte) (ScanPage, error)
	ScanVersioned(ctx context.Context, table domain.LogicalTable, limit int, startKey []byte) (VersionedPage, error)
}

// VersionedItem é um item com a chave canônica e a versão _dr_v (0 = sem versão).
type VersionedItem struct {
	Key     []byte
	Item    []byte // JSON do DynamoDB, sem _dr_v
	Version domain.Index
}

// VersionedPage é uma página do ScanVersioned; LastKey nil no fim.
type VersionedPage struct {
	Items   []VersionedItem
	LastKey []byte
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

// GatePublisher leva o gate da líder às outras células (G-06), junto com o piso de
// retenção do disco (RT-15). Nunca bloqueia e nunca perde o valor mais recente.
type GatePublisher interface {
	Publish(gate, floor domain.Index)
}

// ErrNotRetained: a faixa pedida já saiu do disco retido (RT-16).
var ErrNotRetained = errors.New("faixa do log fora do disco retido")

// SnapshotPolicy decide se um MsgSnap recebido chega ao Raft (RT-18, SG-19): só com o
// aplicado local ≥ índice do marcador. Behind avisa que a célula está atrás da cauda.
type SnapshotPolicy interface {
	Applied() domain.Index
	Behind(target domain.Index)
}

// LogSource lê entradas commitadas e definitivas do log, da RAM ou do disco (RT-16).
type LogSource interface {
	Read(ctx context.Context, lo, hi domain.Index, maxBytes uint64) ([]domain.Entry, error)
	FirstRetained() domain.Index
}

// RetentionPins segura o disco do log a partir de um índice enquanto alguém lê dele
// (leitura do disco, canal de recuperação, reconstrução). O pino segura as entradas
// depois de idx. Has diz se o pino do dono ainda existe (o teto duro solta os de
// reconstrução, RT-15).
type RetentionPins interface {
	Pin(owner string, idx domain.Index) (release func())
	PinFor(owner string, idx domain.Index, ttl time.Duration)
	Unpin(owner string)
	Has(owner string) bool
}

// Erros do canal de recuperação (RT-19).
var (
	// ErrSourceNotReady: a fonte não está em normal (ou ainda está na pré-partida).
	ErrSourceNotReady = errors.New("fonte fora de normal")
	// ErrSourceGone: a fonte caiu ou o transporte falhou no meio do fluxo.
	ErrSourceGone = errors.New("fonte indisponível")
)

// RetainedInfo é a resposta do Retained (RT-19): o que a célula retém e em que estado está.
// Na pré-partida (DC-12), só ClusterID, EmptyVolume, BootToken e State ("partida").
type RetainedInfo struct {
	Node          uint64
	FirstRetained domain.Index
	Commit        domain.Index
	Gate          domain.Index
	State         string // normal, recuperando, reconstruindo ou partida
	Leader        bool
	ClusterID     string
	Epoch         uint64
	EmptyVolume   bool
	BootToken     string
	Genesis       map[uint64]string
}

// CatchUpBatch é um lote do CatchUp: entradas contíguas e o gate da fonte que as cobre
// (DC-02, SG-21).
type CatchUpBatch struct {
	Entries    []domain.Entry
	SourceGate domain.Index
}

// CopyPage é uma página do CopyTable (AP-18). F e KeySchema vêm na primeira página de cada
// tabela; TableDone fecha a tabela e Done, a cópia inteira.
type CopyPage struct {
	F         domain.Index
	Table     domain.LogicalTable
	KeySchema domain.KeySchema
	Items     []VersionedItem
	TableDone bool
	Done      bool
}

// EntryStream é o fluxo do CatchUp; Recv devolve io.EOF ao alcançar o fim pedido.
type EntryStream interface {
	Recv() (CatchUpBatch, error)
	Close()
}

// CopyStream é o fluxo do CopyTable; Recv devolve io.EOF depois do Done.
type CopyStream interface {
	Recv() (CopyPage, error)
	Close()
}

// RecoveryClient é o canal de recuperação entre células (RT-19): Retained, CatchUp e
// CopyTable. Os códigos do fio viram ErrNotRetained, ErrSourceNotReady e
// usecase.ErrRebuildAborted; falha de transporte vira ErrSourceGone.
type RecoveryClient interface {
	Retained(ctx context.Context, node uint64) (RetainedInfo, error)
	CatchUp(ctx context.Context, node uint64, from, to domain.Index) (EntryStream, error)
	CopyTable(ctx context.Context, node uint64, tables []domain.LogicalTable) (CopyStream, error)
}

// RetentionFloorSink recebe, na follower, o piso de retenção publicado pela líder (RT-15).
type RetentionFloorSink interface {
	SetFloor(domain.Index)
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

	// Log do Raft limitado (O-37, item 6).
	Snapshot      domain.Index    // índice do último snapshot-marcador
	MemEntries    uint64          // entradas no MemoryStorage
	MemBytes      int64           // bytes das entradas no MemoryStorage
	RetainedBytes int64           // bytes dos arquivos .wal no disco
	RetainedFiles int             // arquivos .wal no disco
	Floor         domain.Index    // piso de retenção (calculado na líder, recebido na follower)
	Pins          int             // pinos ativos
	HardCeiling   bool            // disco no teto duro
	HeldByApplied bool            // no teto duro, quem segura o disco é o aplicado local
	BehindTail    bool            // a célula recebeu um marcador à frente do seu aplicado
	Window        map[uint64]bool // réplicas dentro da janela de retenção (só na líder)

	MemMax    int64            // RAFT_RAM_MAX
	MemFirst  domain.Index     // primeiro índice no MemoryStorage
	MemByPeer map[uint64]int64 // bytes na RAM desde o Match de cada par (só na líder)
}

// PeerAppliedSink recebe o aplicado informado por uma follower na resposta do GateUpdate
// (DC-15). Só para medir: não decide nada.
type PeerAppliedSink interface {
	PeerApplied(id uint64, applied domain.Index, at time.Time)
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
	Ops []domain.Op
	// Forwarded marca a requisição que já foi encaminhada por um follower: nunca é
	// reencaminhada (limite de um salto, ADR-039).
	Forwarded bool
}

type WriteResult struct {
	Index domain.Index
}

type Writer interface {
	Write(ctx context.Context, r WriteRequest) (WriteResult, error)
}

// Reader é a leitura forte (A.6): sempre forte, uma barreira por chamada.
type Reader interface {
	Get(ctx context.Context, table domain.LogicalTable, key []byte) ([]byte, error)
	Scan(ctx context.Context, table domain.LogicalTable, limit int, startKey []byte) (ScanPage, error)
}

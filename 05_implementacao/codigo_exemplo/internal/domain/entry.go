package domain

// Index é a posição no log; também é a versão do item no banco (6.4).
type Index uint64

type Term uint64

type ShardID uint32

type EntryKind uint8

const (
	KindWrite   EntryKind = iota + 1 // escrita do cliente
	KindDegrade                      // DEGRADE(epoch, fromIndex)
	KindRestore                      // RESTORE(epoch)
)

type OpKind uint8

const (
	OpPut OpKind = iota + 1
	OpDelete
)

// Op é uma mutação já normalizada: tabela lógica, chave e imagem final.
type Op struct {
	Table LogicalTable
	Kind  OpKind
	Key   []byte // chave serializada (partição + ordenação)
	Item  []byte // post-image no formato DynamoDB JSON; vazio no delete
}

// Entry é a unidade replicada. Uma chamada BatchWriteItem vira UMA Entry (achado 2).
type Entry struct {
	Index    Index
	Term     Term
	Kind     EntryKind
	Shard    ShardID
	Ops      []Op
	Epoch    uint64 // só para Degrade e Restore
	ClientID string // id estável do SDK, quando houver (D11)
}

// LogicalTable é o nome com o marcador {cell}, ex.: "cliente_cell_{cell}".
type LogicalTable string

// Package raftstore é o escritor do log do Raft de UM shard (ADR-056, 6.10).
//
//   - Group commit por shard, sem espera fixa: assim que o fsync anterior termina, junta
//     tudo o que está pendente (até MaxBatchBytes) num único Persist, que no adaptador
//     real é um wal.Save do etcd com um fdatasync. Com pouco tráfego o lote é de uma entrada.
//   - Nada sai antes do fsync: as respostas de cada lote (MsgAppResp, votos, auto-ACK da
//     líder, Advance) só são liberadas depois de Persist e Publish, na ordem de chegada.
//   - Erro de escrita ou de fsync é fatal (ADR-034) e não libera nenhuma resposta.
//
// A forma é a do AsyncStorageWrites (fila de lotes com respostas presas): no modo
// síncrono o laço do shard espera o Release do próprio lote; se o spike S1b adotar o
// modo assíncrono, muda só a ligação no adaptador etcdraft.
package raftstore

import (
	"context"
	"errors"
	"fmt"
	"os"

	"example.com/datareplication/internal/domain"
)

// ErrGap: o lote seguinte começa depois da cabeça do anterior, deixando um buraco.
// É bug de quem chama; nunca algo para tolerar.
var ErrGap = errors.New("raftstore: lote fora de sequência")

// Record é uma entrada do log do Raft, já serializada (payload opaco para o escritor).
type Record struct {
	Index domain.Index
	Term  domain.Term
	Data  []byte
}

// HardState é o estado do consenso que precisa estar no disco antes de qualquer voto.
type HardState struct {
	Term   domain.Term
	Vote   string
	Commit domain.Index
}

// Batch é o que o laço do shard entrega ao escritor a cada Ready.
type Batch struct {
	Entries []Record
	State   *HardState // nil se não mudou
	// Barrier marca um lote que não pode ser juntado a outros (snapshot): vai sozinho.
	Barrier bool
	// Release entrega as respostas presas a este lote. Chamado só depois do fsync.
	Release func()
}

func (b Batch) bytes() int {
	n := 0
	for _, r := range b.Entries {
		n += len(r.Data)
	}
	return n
}

// Storage é o que o escritor precisa do armazenamento.
type Storage interface {
	// Persist grava as entradas e o HardState e faz fdatasync. No adaptador real é um
	// único wal.Save do etcd (que só faz fsync se há entradas ou mudou termo ou voto).
	Persist(st *HardState, ents []Record) error
	// Publish torna as entradas visíveis ao Raft (MemoryStorage.Append e SetHardState).
	// Sempre depois do Persist e antes de liberar as respostas.
	Publish(st *HardState, ents []Record) error
}

// Options do escritor.
type Options struct {
	// MaxBatchBytes é o teto do lote (RAFT_BATCH_MAX_BYTES, 1 MiB). Uma entrada maior
	// que o teto vai sozinha.
	MaxBatchBytes int
	// QueueLen limita a fila de lotes. O limite em bytes das entradas não persistidas é
	// do Raft (MaxUncommittedEntriesSize, 32 MiB por shard).
	QueueLen int
	// Fatal encerra o processo. Padrão: os.Exit(2). Os testes injetam outro.
	Fatal func(error)
	// OnBatch é chamado depois de cada fsync (métricas raft_batch_entries e raft_batch_bytes).
	OnBatch func(entries, bytes int)
}

// Writer é o escritor do log do Raft de um shard. Submit pode ser chamado de uma
// goroutine; Run roda em outra.
type Writer struct {
	st    Storage
	opt   Options
	in    chan Batch
	carry *Batch // lote lido da fila que não coube no grupo anterior
}

// New cria o escritor.
func New(st Storage, opt Options) *Writer {
	if opt.MaxBatchBytes <= 0 {
		opt.MaxBatchBytes = 1 << 20
	}
	if opt.QueueLen <= 0 {
		opt.QueueLen = 64
	}
	if opt.Fatal == nil {
		opt.Fatal = func(err error) {
			fmt.Fprintln(os.Stderr, "raftstore: erro fatal:", err)
			os.Exit(2)
		}
	}
	return &Writer{st: st, opt: opt, in: make(chan Batch, opt.QueueLen)}
}

// Submit enfileira um lote. A ordem de Submit é a ordem de gravação e de Release.
func (w *Writer) Submit(ctx context.Context, b Batch) error {
	select {
	case w.in <- b:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run grava os lotes até o contexto acabar. Em erro de escrita, chama Fatal, não libera
// nenhuma resposta do grupo e devolve o erro.
func (w *Writer) Run(ctx context.Context) error {
	for {
		group, err := w.next(ctx)
		if err != nil {
			return err
		}
		if err := w.write(group); err != nil {
			w.opt.Fatal(err)
			return err
		}
	}
}

// next espera o primeiro lote e junta, sem esperar, os que já estão na fila.
func (w *Writer) next(ctx context.Context) ([]Batch, error) {
	var first Batch
	if w.carry != nil {
		first, w.carry = *w.carry, nil
	} else {
		select {
		case first = <-w.in:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	group := []Batch{first}
	if first.Barrier {
		return group, nil
	}
	size := first.bytes()
	for {
		select {
		case b := <-w.in:
			if b.Barrier || size+b.bytes() > w.opt.MaxBatchBytes {
				w.carry = &b
				return group, nil
			}
			group = append(group, b)
			size += b.bytes()
		default:
			return group, nil // sem espera fixa: o lote é o que já chegou
		}
	}
}

func (w *Writer) write(group []Batch) error {
	st, ents, err := Merge(group)
	if err != nil {
		return err
	}
	if err := w.st.Persist(st, ents); err != nil {
		return fmt.Errorf("raftstore: persist: %w", err)
	}
	if err := w.st.Publish(st, ents); err != nil {
		return fmt.Errorf("raftstore: publish: %w", err)
	}
	if w.opt.OnBatch != nil {
		n := 0
		for _, r := range ents {
			n += len(r.Data)
		}
		w.opt.OnBatch(len(ents), n)
	}
	for _, b := range group { // só agora: o grupo inteiro está no disco
		if b.Release != nil {
			b.Release()
		}
	}
	return nil
}

// Merge junta os lotes na ordem. Um lote que reescreve índices (troca de termo) corta o
// que veio antes a partir do primeiro índice dele, como o Raft exige ("descartar as
// entradas persistidas com índice >= i"). Vale o último HardState não nulo.
func Merge(group []Batch) (*HardState, []Record, error) {
	var st *HardState
	var ents []Record
	for _, b := range group {
		if b.State != nil {
			st = b.State
		}
		if len(b.Entries) == 0 {
			continue
		}
		first := b.Entries[0].Index
		if len(ents) > 0 {
			head := ents[len(ents)-1].Index
			switch {
			case first > head+1:
				return nil, nil, fmt.Errorf("%w: cabeça %d, lote começa em %d", ErrGap, head, first)
			case first <= ents[0].Index:
				ents = ents[:0]
			case first <= head:
				ents = ents[:first-ents[0].Index]
			}
		}
		ents = append(ents, b.Entries...)
	}
	return st, ents, nil
}

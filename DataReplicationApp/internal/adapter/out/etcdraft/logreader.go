package etcdraft

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.etcd.io/etcd/client/pkg/v3/fileutil"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// ErrNotRetained: a faixa pedida já saiu do disco retido (RT-16).
var ErrNotRetained = port.ErrNotRetained

// LogReader é o único leitor de entradas commitadas (RT-16): lê da RAM quando o índice
// ainda está no MemoryStorage e, abaixo dela, dos segmentos .wal em modo leitura, com o
// decodificador do etcd. Nunca roda no laço do Ready: quem chama é o CommitPump (e, no
// Incremento B, o canal de recuperação).
//
// Regra da entrada final (SG-20): o WAL pode ter uma versão não commitada de um índice,
// reescrita depois por outra líder. O leitor guarda as entradas num buffer, troca o buffer
// a partir de i quando o índice i reaparece, e só entrega as entradas ≤ c ao ler um
// HardState com commit = c. Assim só sai entrada commitada e definitiva.
type LogReader struct {
	node *Node
	ret  *Retention
	dir  string
}

var _ port.LogSource = (*LogReader)(nil)

// Read devolve as entradas commitadas de lo a hi (inclusive), até maxBytes (pelo menos
// uma). Abaixo da RAM, devolve só a parte do disco; o chamador pede o resto em seguida.
func (r *LogReader) Read(ctx context.Context, lo, hi domain.Index, maxBytes uint64) ([]domain.Entry, error) {
	if c := r.node.CommitIndex(); hi > c {
		hi = c
	}
	if lo > hi {
		return nil, nil
	}
	ramFirst := r.node.ramFirst()
	if lo >= ramFirst {
		ents, err := r.node.storage.Entries(uint64(lo), uint64(hi)+1, maxBytes)
		if err == nil {
			return convertEntries(ents)
		}
		if !errors.Is(err, raft.ErrCompacted) {
			return nil, fmt.Errorf("etcdraft: ler entradas [%d, %d]: %w", lo, hi, err)
		}
		ramFirst = r.node.ramFirst() // compactada entre a conferência e a leitura: vai ao disco
	}
	return r.readDisk(ctx, lo, min(hi, ramFirst-1), maxBytes)
}

// FirstRetained é o primeiro índice que ainda pode estar no disco. O snapshot sintético
// de partida (índice 1) nunca é uma entrada.
func (r *LogReader) FirstRetained() domain.Index {
	segs, err := r.ret.Segments()
	if err != nil {
		return bootIndex + 1
	}
	return max(domain.RetentionView{Segments: segs}.FirstRetained(), bootIndex+1)
}

// readDisk lê [lo, to] dos segmentos .wal, com um pino em lo − 1 enquanto lê.
func (r *LogReader) readDisk(ctx context.Context, lo, to domain.Index, maxBytes uint64) ([]domain.Entry, error) {
	release := r.ret.Pin(r.ret.readerOwner(), lo-1)
	defer release()
	segs, err := r.ret.Segments()
	if err != nil {
		return nil, err
	}
	start := -1
	for k, s := range segs {
		if s.First <= lo {
			start = k
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("%w: índice %d abaixo do primeiro segmento", ErrNotRetained, lo)
	}
	var files []*os.File
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	var readers []fileutil.FileReader
	for _, s := range segs[start:] {
		f, err := os.Open(filepath.Join(r.dir, segmentName(s.Seq, uint64(s.First))))
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: segmento removido durante a leitura", ErrNotRetained)
		}
		if err != nil {
			return nil, fmt.Errorf("etcdraft: abrir o WAL: %w", err)
		}
		files = append(files, f)
		readers = append(readers, fileutil.NewFileReader(f))
	}
	out, err := decodeCommitted(ctx, readers, lo, to, maxBytes)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: índice %d não encontrado no disco", ErrNotRetained, lo)
	}
	return out, nil
}

// decodeCommitted aplica a regra da entrada final aos registros do WAL e devolve as
// entradas commitadas e contíguas a partir de lo, até to ou maxBytes.
func decodeCommitted(ctx context.Context, readers []fileutil.FileReader, lo, to domain.Index, maxBytes uint64) ([]domain.Entry, error) {
	dec := wal.NewDecoder(readers...)
	var (
		buf      []*raftpb.Entry
		out      []domain.Entry
		outBytes uint64
		lastOut  = lo - 1
		rec      = &walpb.Record{}
	)
	for n := 0; ; n++ {
		if n%1000 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		err := dec.Decode(rec)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return out, nil // fim do que está gravado (a cauda pré-alocada ou em escrita)
		}
		if err != nil {
			return nil, fmt.Errorf("etcdraft: ler o WAL: %w", err)
		}
		switch rec.GetType() {
		case wal.CrcType:
			dec.UpdateCRC(rec.GetCrc())
		case wal.EntryType:
			e := wal.MustUnmarshalEntry(rec.GetData())
			idx := domain.Index(e.GetIndex())
			if idx < lo || idx <= lastOut {
				continue
			}
			// Um índice que reaparece é a versão nova: descarta o buffer a partir dele.
			for len(buf) > 0 && domain.Index(buf[len(buf)-1].GetIndex()) >= idx {
				buf = buf[:len(buf)-1]
			}
			buf = append(buf, e)
		case wal.StateType:
			commit := domain.Index(wal.MustUnmarshalState(rec.GetData()).GetCommit())
			for len(buf) > 0 && domain.Index(buf[0].GetIndex()) <= commit && domain.Index(buf[0].GetIndex()) <= to {
				e := buf[0]
				buf = buf[1:]
				idx := domain.Index(e.GetIndex())
				if idx != lastOut+1 {
					return nil, fmt.Errorf("logreader: buraco em %d", lastOut+1)
				}
				de, err := toDomain(e)
				if err != nil {
					return nil, err
				}
				out = append(out, de)
				outBytes += uint64(proto.Size(e))
				lastOut = idx
				if lastOut == to || outBytes >= maxBytes {
					return out, nil
				}
			}
		}
	}
}

// convertEntries traduz as entradas do Raft para o domínio.
func convertEntries(ents []*raftpb.Entry) ([]domain.Entry, error) {
	out := make([]domain.Entry, 0, len(ents))
	for _, e := range ents {
		de, err := toDomain(e)
		if err != nil {
			return nil, err
		}
		out = append(out, de)
	}
	return out, nil
}

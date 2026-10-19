// Package progress guarda o progresso do applier: a marca d'água (índice, ordem) até a
// qual tudo está no banco da célula (ADR-048, ADR-058). É um registro de 16 bytes com
// CRC, no volume do log do Raft e sem fsync: perdido numa queda de host, o replay a partir
// do WAL do Raft só fica mais longo, e ele é idempotente pela versão. Só o Sync, chamado
// pelo Compactor antes de cada snapshot e liberação de disco, faz fsync (AP-06).
package progress

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

var _ port.ApplyProgress = (*File)(nil)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

const recordLen = 16 // índice u64 | ordem u32 | crc32c u32

// OrdComplete reexporta port.OrdComplete: a entrada inteira já está no banco.
const OrdComplete = port.OrdComplete

// File é o registro de progresso de UM shard.
type File struct {
	f *os.File

	mu      sync.Mutex
	last    domain.Index  // maior índice gravado (pelo SaveProgress ou lido na abertura)
	durable atomic.Uint64 // maior índice que já passou por um fsync (SG-18)
}

// Open abre (ou cria) o arquivo de progresso em path.
func Open(path string) (*File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	p := &File{f: f}
	// O registro lido na abertura conta como gravado, não como durável: só o próximo Sync
	// prova que ele está no disco.
	if idx, _, err := p.LoadProgress(); err == nil {
		p.last = idx
	}
	return p, nil
}

// SaveProgress grava (índice, ordem) no lugar do registro anterior, sem fsync.
func (p *File) SaveProgress(idx domain.Index, ord uint32) error {
	var b [recordLen]byte
	binary.LittleEndian.PutUint64(b[0:8], uint64(idx))
	binary.LittleEndian.PutUint32(b[8:12], ord)
	binary.LittleEndian.PutUint32(b[12:16], crc32.Checksum(b[:12], castagnoli))
	if _, err := p.f.WriteAt(b[:], 0); err != nil {
		return err
	}
	p.mu.Lock()
	p.last = max(p.last, idx)
	p.mu.Unlock()
	return nil
}

// Sync faz fsync do registro e devolve o índice durável: o maior índice gravado antes do
// fsync. O fsync roda fora da trava, então o applier nunca espera por ele.
func (p *File) Sync() (domain.Index, error) {
	p.mu.Lock()
	last := p.last
	p.mu.Unlock()
	if err := p.f.Sync(); err != nil {
		return p.Durable(), err
	}
	for {
		cur := p.durable.Load()
		if uint64(last) <= cur || p.durable.CompareAndSwap(cur, uint64(last)) {
			break
		}
	}
	return p.Durable(), nil
}

// Durable é o maior índice que já passou por um fsync; nunca recua.
func (p *File) Durable() domain.Index { return domain.Index(p.durable.Load()) }

// LoadProgress devolve (0, 0) se o registro não existe ou não confere: o applier recomeça
// do início do que o Raft guarda, de forma idempotente.
func (p *File) LoadProgress() (domain.Index, uint32, error) {
	var b [recordLen]byte
	if _, err := p.f.ReadAt(b[:], 0); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	if crc32.Checksum(b[:12], castagnoli) != binary.LittleEndian.Uint32(b[12:16]) {
		return 0, 0, nil
	}
	return domain.Index(binary.LittleEndian.Uint64(b[0:8])), binary.LittleEndian.Uint32(b[8:12]), nil
}

// Close fecha o arquivo.
func (p *File) Close() error { return p.f.Close() }

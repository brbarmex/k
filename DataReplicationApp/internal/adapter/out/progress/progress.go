// Package progress guarda o progresso do applier: a marca d'água (índice, ordem) até a
// qual tudo está no banco da célula (ADR-048, ADR-058). É um registro de 16 bytes com
// CRC, no volume do log do Raft e sem fsync: perdido numa queda de host, o replay a partir
// do WAL do Raft só fica mais longo, e ele é idempotente pela versão.
package progress

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

var _ port.ApplyProgress = (*File)(nil)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

const recordLen = 16 // índice u64 | ordem u32 | crc32c u32

// File é o registro de progresso de UM shard.
type File struct {
	f *os.File
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
	return &File{f: f}, nil
}

// SaveProgress grava (índice, ordem) no lugar do registro anterior, sem fsync.
func (p *File) SaveProgress(idx domain.Index, ord uint32) error {
	var b [recordLen]byte
	binary.LittleEndian.PutUint64(b[0:8], uint64(idx))
	binary.LittleEndian.PutUint32(b[8:12], ord)
	binary.LittleEndian.PutUint32(b[12:16], crc32.Checksum(b[:12], castagnoli))
	_, err := p.f.WriteAt(b[:], 0)
	return err
}

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

package etcdraft

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"example.com/datareplication/internal/domain"
)

// O arquivo do marcador (DC-06): o registro de snapshot do WAL do etcd só guarda índice,
// termo e ConfState, então o dado do marcador (cluster_id, epoch, confirmado) fica num
// arquivo próprio no volume do Raft, ao lado do progress. Formato: 8 bytes do índice do
// snapshot (little endian) + domain.SnapshotMarker.Encode() (JSON + CRC32-C).

// writeMarker grava o marcador de forma atômica: tmp, fsync, rename e fsync do diretório.
// Um erro é fatal para o chamador (como o fsync do WAL, RT-05).
func writeMarker(path string, m domain.SnapshotMarker, idx domain.Index) error {
	if path == "" {
		return nil // sem arquivo de marcador (testes)
	}
	b := binary.LittleEndian.AppendUint64(nil, uint64(idx))
	b = append(b, m.Encode()...)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("etcdraft: marcador: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("etcdraft: marcador: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("etcdraft: fsync do marcador: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("etcdraft: marcador: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("etcdraft: marcador: %w", err)
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("etcdraft: marcador: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("etcdraft: fsync do diretório do marcador: %w", err)
	}
	return nil
}

// readMarker lê o marcador. Ausente: o marcador da partida do zero (versão 1, o cluster da
// configuração, epoch 1, nada confirmado). Ilegível: domain.ErrBadMarker (fatal: o volume
// precisa de task clean).
func readMarker(path, clusterID string) (domain.SnapshotMarker, domain.Index, error) {
	zero := domain.SnapshotMarker{Version: domain.MarkerVersion, ClusterID: clusterID, Epoch: 1}
	if path == "" {
		return zero, 0, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return zero, 0, nil
	}
	if err != nil {
		return domain.SnapshotMarker{}, 0, fmt.Errorf("etcdraft: marcador: %w", err)
	}
	if len(b) < 8 {
		return domain.SnapshotMarker{}, 0, fmt.Errorf("%w: arquivo curto; rode task clean", domain.ErrBadMarker)
	}
	m, err := domain.DecodeMarker(b[8:])
	if err != nil {
		return domain.SnapshotMarker{}, 0, fmt.Errorf("%w; rode task clean", err)
	}
	return m, domain.Index(binary.LittleEndian.Uint64(b[:8])), nil
}

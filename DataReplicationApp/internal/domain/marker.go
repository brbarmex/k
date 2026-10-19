package domain

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
)

// MarkerVersion é a versão do registro do snapshot-marcador (RT-11).
const MarkerVersion = 1

// ErrBadMarker: o marcador do snapshot não confere (curto, CRC, versão ou JSON).
var ErrBadMarker = errors.New("marcador do snapshot inválido")

var markerCRC = crc32.MakeTable(crc32.Castagnoli)

// SnapshotMarker é o dado do snapshot do Raft (D4, DM, RT-11): o snapshot não carrega os
// dados das tabelas, que vivem no banco da célula. Leva só a identidade do grupo e da
// história e o índice confirmado no momento do snapshot, que nunca recua (G6).
type SnapshotMarker struct {
	Version   uint32 `json:"versao"`
	ClusterID string `json:"cluster_id"`
	Epoch     uint64 `json:"epoch"`
	Confirmed Index  `json:"confirmado"`
}

// Encode devolve o JSON do marcador seguido de 4 bytes do CRC32-C (little endian) do JSON.
func (m SnapshotMarker) Encode() []byte {
	b, _ := json.Marshal(m) // struct sem tipos que falham
	return binary.LittleEndian.AppendUint32(b, crc32.Checksum(b, markerCRC))
}

// DecodeMarker valida o CRC e a versão antes de devolver o marcador (N-04: o marcador
// recebido é validado antes de qualquer efeito).
func DecodeMarker(b []byte) (SnapshotMarker, error) {
	if len(b) < 5 {
		return SnapshotMarker{}, ErrBadMarker
	}
	body, sum := b[:len(b)-4], binary.LittleEndian.Uint32(b[len(b)-4:])
	if crc32.Checksum(body, markerCRC) != sum {
		return SnapshotMarker{}, ErrBadMarker
	}
	var m SnapshotMarker
	if err := json.Unmarshal(body, &m); err != nil {
		return SnapshotMarker{}, fmt.Errorf("%w: %v", ErrBadMarker, err)
	}
	if m.Version != MarkerVersion {
		return SnapshotMarker{}, fmt.Errorf("%w: versão %d", ErrBadMarker, m.Version)
	}
	return m, nil
}

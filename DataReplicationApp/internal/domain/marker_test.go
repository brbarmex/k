package domain

import (
	"errors"
	"testing"
)

// O-31, RT-11: o marcador vai e volta; CRC corrompido, versão errada e registro curto recusam.
func TestMarkerRoundTrip(t *testing.T) {
	m := SnapshotMarker{Version: MarkerVersion, ClusterID: "poc-local", Epoch: 1, Confirmed: 4242}
	got, err := DecodeMarker(m.Encode())
	if err != nil || got != m {
		t.Fatalf("DecodeMarker = %+v, %v; quer %+v", got, err, m)
	}
	bad := m.Encode()
	bad[3] ^= 0xff
	if _, err := DecodeMarker(bad); !errors.Is(err, ErrBadMarker) {
		t.Fatalf("CRC corrompido: erro = %v", err)
	}
	if _, err := DecodeMarker(SnapshotMarker{Version: 2, ClusterID: "x"}.Encode()); !errors.Is(err, ErrBadMarker) {
		t.Fatalf("versão 2: erro = %v", err)
	}
	if _, err := DecodeMarker([]byte{1, 2, 3, 4}); !errors.Is(err, ErrBadMarker) {
		t.Fatalf("registro curto: erro = %v", err)
	}
	if _, err := DecodeMarker(nil); !errors.Is(err, ErrBadMarker) {
		t.Fatalf("vazio: erro = %v", err)
	}
}

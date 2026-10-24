package peerv1

import "testing"

// DC-15: a resposta do GateUpdate é compatível nos dois sentidos durante a rolagem.
func TestGateUpdateResponseCompat(t *testing.T) {
	var c Codec

	var fromOld GateUpdateResponse // follower antiga responde Empty: {}
	if err := c.Unmarshal([]byte("{}"), &fromOld); err != nil || fromOld.Applied != nil {
		t.Fatalf("resposta antiga: %+v %v", fromOld, err)
	}

	seven := uint64(7)
	b, err := c.Marshal(&GateUpdateResponse{Applied: &seven})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Unmarshal(b, &Empty{}); err != nil { // líder antiga lê como Empty
		t.Fatalf("líder antiga: %v", err)
	}

	zero := uint64(0)
	b, _ = c.Marshal(&GateUpdateResponse{Applied: &zero})
	var got GateUpdateResponse
	if err := c.Unmarshal(b, &got); err != nil || got.Applied == nil || *got.Applied != 0 {
		t.Fatalf("aplicado 0 tem de continuar 0, não nil: %+v %v", got, err)
	}
}

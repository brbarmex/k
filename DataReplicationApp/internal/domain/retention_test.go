package domain

import (
	"reflect"
	"testing"
)

func segs() []Segment {
	return []Segment{{0, 0, 100}, {1, 101, 200}, {2, 301, 300}, {3, 601, 50}}
}

// O-31, RT-15: o limite é o menor entre aplicado durável, piso, snapshot e pino.
func TestRetentionLimit(t *testing.T) {
	v := RetentionView{AppliedDurable: 500, Floor: 400, Snapshot: 450}
	if v.Limit() != 400 {
		t.Fatalf("Limit = %d", v.Limit())
	}
	v.MinPin = 120
	if v.Limit() != 120 {
		t.Fatalf("Limit com pino = %d", v.Limit())
	}
	v.MinPin, v.AppliedDurable = 0, 10 // SG-18: o aplicado durável segura
	if v.Limit() != 10 {
		t.Fatalf("Limit com aplicado atrás = %d", v.Limit())
	}
	if (RetentionView{AppliedDurable: 500, Snapshot: 450}).Limit() != 0 {
		t.Fatal("sem piso recebido nada é liberado")
	}
}

func TestRetainedSince(t *testing.T) {
	v := RetentionView{Segments: segs()}
	for _, tc := range []struct {
		i    Index
		want int64
	}{{0, 650}, {50, 650}, {101, 550}, {300, 550}, {301, 350}, {700, 50}} {
		if got := v.RetainedSince(tc.i); got != tc.want {
			t.Errorf("RetainedSince(%d) = %d, quer %d", tc.i, got, tc.want)
		}
	}
	if (RetentionView{}).RetainedSince(5) != 0 {
		t.Fatal("lista vazia")
	}
}

// O-31: a cauda e o segmento anterior a ela nunca saem; o arquivo com o índice no limite fica.
func TestRemovable(t *testing.T) {
	v := RetentionView{Segments: segs()}
	if got := v.Removable(100); len(got) != 0 {
		t.Fatalf("limite 100 (o seguinte começa em 101): %v", got)
	}
	if got := v.Removable(101); !reflect.DeepEqual(got, segs()[:1]) {
		t.Fatalf("limite 101: %v", got)
	}
	if got := v.Removable(10_000); !reflect.DeepEqual(got, segs()[:2]) {
		t.Fatalf("limite alto protege a cauda e o anterior: %v", got)
	}
	if got := (RetentionView{Segments: segs()[:1]}).Removable(10_000); len(got) != 0 {
		t.Fatalf("um segmento: %v", got)
	}
	if got := (RetentionView{}).Removable(10_000); len(got) != 0 {
		t.Fatalf("lista vazia: %v", got)
	}
}

func TestFirstRetained(t *testing.T) {
	if (RetentionView{Segments: segs()}).FirstRetained() != 0 {
		t.Fatal("primeiro segmento")
	}
	if (RetentionView{Snapshot: 9}).FirstRetained() != 10 {
		t.Fatal("sem segmentos: snapshot + 1")
	}
}

// O-31, DC-08: par fora da janela não segura o piso; todos fora → snapshot; a líder é ignorada.
func TestLeaderFloor(t *testing.T) {
	v := RetentionView{Segments: segs(), Snapshot: 640}
	floor, in := LeaderFloor(map[uint64]Index{1: 650, 2: 620, 3: 50}, 1, v, 400)
	if floor != 620 || !in[2] || in[3] || len(in) != 2 {
		t.Fatalf("piso %d janela %v", floor, in)
	}
	floor, in = LeaderFloor(map[uint64]Index{1: 650, 2: 50, 3: 60}, 1, v, 400)
	if floor != 640 || in[2] || in[3] {
		t.Fatalf("todos fora: piso %d janela %v", floor, in)
	}
	floor, _ = LeaderFloor(map[uint64]Index{1: 5}, 1, v, 400)
	if floor != 640 {
		t.Fatalf("só a líder: piso %d", floor)
	}
}

// DC-20: a saída da janela não tem volta. Depois que o segmento com o Match do par saiu, os
// bytes contados do primeiro segmento que sobrou cabem de novo, mas o par continua fora.
func TestLeaderFloorWindowIsSticky(t *testing.T) {
	v := RetentionView{Snapshot: 900, Segments: []Segment{{Seq: 5, First: 600, Bytes: 100}, {Seq: 6, First: 700, Bytes: 100}}}
	floor, in := LeaderFloor(map[uint64]Index{1: 900, 2: 550, 3: 880}, 1, v, 400)
	if in[2] || floor != 880 {
		t.Fatalf("par com o Match abaixo do primeiro retido: janela %v piso %d", in, floor)
	}
	if floor, in := LeaderFloor(map[uint64]Index{1: 900, 2: 599, 3: 880}, 1, v, 400); !in[2] || floor != 599 {
		t.Fatalf("Match + 1 = primeiro retido ainda está na janela: %v %d", in, floor)
	}
	if _, in := LeaderFloor(map[uint64]Index{1: 900, 2: 0, 3: 880}, 1, v, 400); !in[2] {
		t.Fatal("Match 0 (desconhecido) segue a regra dos bytes")
	}
}

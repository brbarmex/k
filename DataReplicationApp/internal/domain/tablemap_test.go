package domain

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestTableMap(t *testing.T) {
	m, err := NewTableMap("1", []string{"t1_cell_{cell}", "cadastro_cliente_cell_{cell}"})
	if err != nil {
		t.Fatal(err)
	}
	// SG-08: "t1_cell_1" com CELL_ID=1 resolve pela lista, não por troca de texto.
	if l, ok := m.Logical("t1_cell_1"); !ok || l != "t1_cell_{cell}" {
		t.Fatalf("t1_cell_1 → %q %v", l, ok)
	}
	if p, ok := m.Physical("t1_cell_{cell}"); !ok || p != "t1_cell_1" {
		t.Fatalf("físico = %q %v", p, ok)
	}
	if _, ok := m.Physical("outra_{cell}"); ok {
		t.Fatal("tabela fora da lista não tem físico")
	}
	if got := m.Tables(); !slices.Equal(got, []LogicalTable{"t1_cell_{cell}", "cadastro_cliente_cell_{cell}"}) {
		t.Fatalf("tabelas = %v", got)
	}

	m2, _ := NewTableMap("2", []string{"cadastro_cliente_cell_{cell}"})
	if _, ok := m2.Logical("cadastro_cliente_cell_1"); ok {
		t.Fatal("tabela de outra célula não pode ser aceita (CA-10)")
	}
	if l, ok := m2.Logical("cadastro_cliente_cell_2"); !ok || l != "cadastro_cliente_cell_{cell}" {
		t.Fatalf("cell 2 → %q %v", l, ok)
	}
}

func TestTableMapRejects(t *testing.T) {
	for _, c := range []struct {
		cell   string
		tables []string
	}{
		{"", []string{"a_{cell}"}},
		{"1", []string{""}},
		{"1", []string{"sem_marcador"}},
		{"1", []string{"dois_{cell}_{cell}"}},
		{"1", []string{"a_{cell}", "a_{cell}"}},
	} {
		if _, err := NewTableMap(c.cell, c.tables); err == nil {
			t.Fatalf("devia recusar %q %v", c.cell, c.tables)
		}
	}
}

func TestKeySchema(t *testing.T) {
	k := KeySchema{Partition: KeyAttr{"pk", "S"}}
	if k.HasSort() || !slices.Equal(k.Names(), []string{"pk"}) {
		t.Fatalf("sem ordenação: %v %v", k.HasSort(), k.Names())
	}
	k.Sort = KeyAttr{"sk", "N"}
	if !k.HasSort() || !slices.Equal(k.Names(), []string{"pk", "sk"}) {
		t.Fatalf("com ordenação: %v %v", k.HasSort(), k.Names())
	}
}

func TestCanonicalKey(t *testing.T) {
	k := KeySchema{Partition: KeyAttr{"pk", "S"}, Sort: KeyAttr{"sk", "N"}}
	got, err := CanonicalKey(k, map[string]json.RawMessage{"sk": json.RawMessage(`{ "N": "1" }`), "pk": json.RawMessage(`{"S":"a"}`), "x": json.RawMessage(`{"S":"y"}`)})
	if err != nil || string(got) != `{"pk":{"S":"a"},"sk":{"N":"1"}}` {
		t.Fatalf("chave = %s %v", got, err)
	}
	if _, err := CanonicalKey(k, map[string]json.RawMessage{"pk": json.RawMessage(`{"S":"a"}`)}); err == nil {
		t.Fatal("chave incompleta devia falhar")
	}
}

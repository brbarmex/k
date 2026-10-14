package domain

import (
	"fmt"
	"strings"
)

// CellMarker é o marcador do id da célula no nome lógico da tabela (ADR-022).
const CellMarker = "{cell}"

// TableMap é a lista permitida de tabelas e a conversão entre o nome físico da célula
// local e o nome lógico (SG-08, AP-05).
//
// O nome lógico de um pedido é achado por comparação com o físico de cada tabela
// permitida, nunca trocando o texto do CELL_ID no nome pedido: com CELL_ID=1, uma troca
// de texto erraria em nomes como "t1_cell_1".
type TableMap struct {
	cell          string
	logical       []LogicalTable
	physToLogical map[string]LogicalTable
}

// NewTableMap valida a lista de tabelas lógicas (cada uma com {cell} exatamente uma vez,
// sem repetição) e monta o mapa para a célula cellID.
func NewTableMap(cellID string, logical []string) (TableMap, error) {
	if cellID == "" {
		return TableMap{}, fmt.Errorf("tablemap: CELL_ID vazio")
	}
	m := TableMap{cell: cellID, physToLogical: make(map[string]LogicalTable, len(logical))}
	seen := make(map[string]bool, len(logical))
	for _, n := range logical {
		if n == "" {
			return TableMap{}, fmt.Errorf("tablemap: nome de tabela vazio")
		}
		if strings.Count(n, CellMarker) != 1 {
			return TableMap{}, fmt.Errorf("tablemap: a tabela %q precisa ter %s exatamente uma vez", n, CellMarker)
		}
		if seen[n] {
			return TableMap{}, fmt.Errorf("tablemap: tabela %q repetida", n)
		}
		seen[n] = true
		phys := strings.Replace(n, CellMarker, cellID, 1)
		if other, dup := m.physToLogical[phys]; dup {
			return TableMap{}, fmt.Errorf("tablemap: %q e %q dão o mesmo nome físico %q", other, n, phys)
		}
		m.physToLogical[phys] = LogicalTable(n)
		m.logical = append(m.logical, LogicalTable(n))
	}
	return m, nil
}

// Logical devolve a tabela lógica do nome físico pedido, só se ele for o físico de uma
// tabela permitida NESTA célula.
func (m TableMap) Logical(physical string) (LogicalTable, bool) {
	l, ok := m.physToLogical[physical]
	return l, ok
}

// Physical devolve o nome físico da tabela lógica na célula local (AP-05).
func (m TableMap) Physical(l LogicalTable) (string, bool) {
	for _, t := range m.logical {
		if t == l {
			return strings.Replace(string(l), CellMarker, m.cell, 1), true
		}
	}
	return "", false
}

// Tables devolve as tabelas lógicas na ordem da configuração.
func (m TableMap) Tables() []LogicalTable { return append([]LogicalTable(nil), m.logical...) }

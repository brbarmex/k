package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// KeyAttr é um atributo da chave primária: nome e tipo escalar do DynamoDB ("S", "N", "B").
type KeyAttr struct {
	Name string
	Type string
}

// KeySchema é a chave primária de uma tabela, lida do banco na partida (DescribeTable).
type KeySchema struct {
	Partition KeyAttr
	Sort      KeyAttr // Name vazio = tabela sem chave de ordenação
}

// HasSort diz se a tabela tem chave de ordenação.
func (k KeySchema) HasSort() bool { return k.Sort.Name != "" }

// Names devolve os nomes dos atributos da chave: partição, depois ordenação.
func (k KeySchema) Names() []string {
	if k.HasSort() {
		return []string{k.Partition.Name, k.Sort.Name}
	}
	return []string{k.Partition.Name}
}

// CanonicalKey monta a chave canônica de Op.Key: JSON do DynamoDB só com os atributos
// da chave, partição antes da ordenação, cada valor já compactado. attrs são os valores
// em JSON do DynamoDB por nome de atributo.
func CanonicalKey(k KeySchema, attrs map[string]json.RawMessage) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, name := range k.Names() {
		raw, ok := attrs[name]
		if !ok {
			return nil, fmt.Errorf("atributo da chave ausente: %s", name)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		qn, _ := json.Marshal(name)
		b.Write(qn)
		b.WriteByte(':')
		if err := json.Compact(&b, raw); err != nil {
			return nil, fmt.Errorf("valor inválido no atributo %s: %w", name, err)
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

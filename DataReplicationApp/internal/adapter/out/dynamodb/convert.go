// Package dynamodb é o banco da célula com aws-sdk-go-v2 (O-05): apply condicional pela
// versão _dr_v, GetItem e Scan consistentes sem _dr_v, e o catálogo de chaves lido com
// DescribeTable na partida.
package dynamodb

import (
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// VersionAttr é o atributo reservado com a versão do item (= índice da entrada).
const VersionAttr = "_dr_v"

// itemFromJSON converte um item em JSON do DynamoDB para o tipo do SDK.
func itemFromJSON(data []byte) (map[string]types.AttributeValue, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("item ilegível: %w", err)
	}
	return mapFromRaw(raw)
}

func mapFromRaw(raw map[string]json.RawMessage) (map[string]types.AttributeValue, error) {
	out := make(map[string]types.AttributeValue, len(raw))
	for name, v := range raw {
		av, err := avFromJSON(v)
		if err != nil {
			return nil, fmt.Errorf("atributo %s: %w", name, err)
		}
		out[name] = av
	}
	return out, nil
}

func avFromJSON(data json.RawMessage) (types.AttributeValue, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if len(m) != 1 {
		return nil, fmt.Errorf("o valor precisa ter exatamente um tipo")
	}
	for typ, v := range m {
		switch typ {
		case "S":
			var s string
			err := json.Unmarshal(v, &s)
			return &types.AttributeValueMemberS{Value: s}, err
		case "N":
			var s string
			err := json.Unmarshal(v, &s)
			return &types.AttributeValueMemberN{Value: s}, err
		case "B":
			var b []byte
			err := json.Unmarshal(v, &b)
			return &types.AttributeValueMemberB{Value: b}, err
		case "BOOL":
			var b bool
			err := json.Unmarshal(v, &b)
			return &types.AttributeValueMemberBOOL{Value: b}, err
		case "NULL":
			var b bool
			err := json.Unmarshal(v, &b)
			return &types.AttributeValueMemberNULL{Value: b}, err
		case "SS":
			var ss []string
			err := json.Unmarshal(v, &ss)
			return &types.AttributeValueMemberSS{Value: ss}, err
		case "NS":
			var ns []string
			err := json.Unmarshal(v, &ns)
			return &types.AttributeValueMemberNS{Value: ns}, err
		case "BS":
			var bs [][]byte
			err := json.Unmarshal(v, &bs)
			return &types.AttributeValueMemberBS{Value: bs}, err
		case "M":
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(v, &raw); err != nil {
				return nil, err
			}
			mm, err := mapFromRaw(raw)
			return &types.AttributeValueMemberM{Value: mm}, err
		case "L":
			var raws []json.RawMessage
			if err := json.Unmarshal(v, &raws); err != nil {
				return nil, err
			}
			l := make([]types.AttributeValue, 0, len(raws))
			for _, r := range raws {
				av, err := avFromJSON(r)
				if err != nil {
					return nil, err
				}
				l = append(l, av)
			}
			return &types.AttributeValueMemberL{Value: l}, nil
		default:
			return nil, fmt.Errorf("tipo desconhecido %q", typ)
		}
	}
	return nil, fmt.Errorf("valor vazio")
}

// itemToJSON converte um item do SDK para JSON do DynamoDB.
func itemToJSON(item map[string]types.AttributeValue) ([]byte, error) {
	m, err := mapToAny(item)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

func mapToAny(item map[string]types.AttributeValue) (map[string]any, error) {
	out := make(map[string]any, len(item))
	for name, av := range item {
		v, err := avToAny(av)
		if err != nil {
			return nil, fmt.Errorf("atributo %s: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}

func avToAny(av types.AttributeValue) (any, error) {
	switch v := av.(type) {
	case *types.AttributeValueMemberS:
		return map[string]any{"S": v.Value}, nil
	case *types.AttributeValueMemberN:
		return map[string]any{"N": v.Value}, nil
	case *types.AttributeValueMemberB:
		return map[string]any{"B": v.Value}, nil
	case *types.AttributeValueMemberBOOL:
		return map[string]any{"BOOL": v.Value}, nil
	case *types.AttributeValueMemberNULL:
		return map[string]any{"NULL": v.Value}, nil
	case *types.AttributeValueMemberSS:
		return map[string]any{"SS": v.Value}, nil
	case *types.AttributeValueMemberNS:
		return map[string]any{"NS": v.Value}, nil
	case *types.AttributeValueMemberBS:
		return map[string]any{"BS": v.Value}, nil
	case *types.AttributeValueMemberM:
		m, err := mapToAny(v.Value)
		return map[string]any{"M": m}, err
	case *types.AttributeValueMemberL:
		l := make([]any, 0, len(v.Value))
		for _, e := range v.Value {
			x, err := avToAny(e)
			if err != nil {
				return nil, err
			}
			l = append(l, x)
		}
		return map[string]any{"L": l}, nil
	default:
		return nil, fmt.Errorf("tipo de atributo não suportado %T", av)
	}
}

// stripVersion tira o atributo reservado da resposta (ADR-051).
func stripVersion(item map[string]types.AttributeValue) map[string]types.AttributeValue {
	delete(item, VersionAttr)
	return item
}

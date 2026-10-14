package dynamohttp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"unicode/utf8"
)

// maxItemSize é o tamanho máximo de item do DynamoDB (400 KB).
const maxItemSize = 400 * 1024

// validateValue confere um valor de atributo em JSON do DynamoDB (exatamente um tipo
// conhecido e bem formado) e devolve o tipo e o tamanho aproximado em bytes, pela regra
// do DynamoDB (nomes e valores). Tudo o que o banco recusaria é recusado aqui (SG-07).
func validateValue(raw json.RawMessage) (string, int, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || len(m) != 1 {
		return "", 0, fmt.Errorf("valor de atributo precisa ter exatamente um tipo")
	}
	for typ, v := range m {
		switch typ {
		case "S":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return "", 0, fmt.Errorf("valor S inválido")
			}
			return typ, len(s), nil
		case "N":
			n, err := number(v)
			return typ, n, err
		case "B":
			b, err := binary(v)
			return typ, b, err
		case "BOOL", "NULL":
			var b bool
			if err := json.Unmarshal(v, &b); err != nil {
				return "", 0, fmt.Errorf("valor %s inválido", typ)
			}
			if typ == "NULL" && !b {
				return "", 0, fmt.Errorf("NULL só aceita true")
			}
			return typ, 1, nil
		case "SS", "NS", "BS":
			var items []json.RawMessage
			if err := json.Unmarshal(v, &items); err != nil || len(items) == 0 {
				return "", 0, fmt.Errorf("conjunto %s vazio ou inválido", typ)
			}
			seen := map[string]bool{}
			size := 0
			for _, it := range items {
				var n int
				var err error
				switch typ {
				case "SS":
					var s string
					err = json.Unmarshal(it, &s)
					n = len(s)
				case "NS":
					n, err = number(it)
				case "BS":
					n, err = binary(it)
				}
				if err != nil {
					return "", 0, err
				}
				if seen[string(it)] {
					return "", 0, fmt.Errorf("conjunto %s com valores repetidos", typ)
				}
				seen[string(it)] = true
				size += n
			}
			return typ, size, nil
		case "M":
			var mm map[string]json.RawMessage
			if err := json.Unmarshal(v, &mm); err != nil {
				return "", 0, fmt.Errorf("valor M inválido")
			}
			size := 3
			for name, e := range mm {
				_, n, err := validateValue(e)
				if err != nil {
					return "", 0, err
				}
				size += len(name) + n + 1
			}
			return typ, size, nil
		case "L":
			var l []json.RawMessage
			if err := json.Unmarshal(v, &l); err != nil {
				return "", 0, fmt.Errorf("valor L inválido")
			}
			size := 3
			for _, e := range l {
				_, n, err := validateValue(e)
				if err != nil {
					return "", 0, err
				}
				size += n + 1
			}
			return typ, size, nil
		default:
			return "", 0, fmt.Errorf("tipo de atributo desconhecido %q", typ)
		}
	}
	return "", 0, fmt.Errorf("valor vazio")
}

func number(v json.RawMessage) (int, error) {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return 0, fmt.Errorf("valor N inválido")
	}
	if _, ok := new(big.Float).SetString(s); !ok {
		return 0, fmt.Errorf("valor N inválido: %q", s)
	}
	return (len(s)+1)/2 + 1, nil
}

func binary(v json.RawMessage) (int, error) {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return 0, fmt.Errorf("valor B inválido")
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0, fmt.Errorf("valor B não é base64")
	}
	return len(b), nil
}

// itemSize soma nomes e valores do item; nomes contam em bytes UTF-8.
func itemSize(item map[string]json.RawMessage) (int, error) {
	size := 0
	for name, v := range item {
		if !utf8.ValidString(name) {
			return 0, fmt.Errorf("nome de atributo inválido")
		}
		_, n, err := validateValue(v)
		if err != nil {
			return 0, fmt.Errorf("atributo %s: %w", name, err)
		}
		size += len(name) + n
	}
	return size, nil
}

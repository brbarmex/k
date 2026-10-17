package dynamohttp

import (
	"encoding/json"
	"strings"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// targetPrefix é o prefixo do header X-Amz-Target da API do DynamoDB.
const targetPrefix = "DynamoDB_20120810."

// Operações atendidas na POC (R.3).
const (
	OpPutItem = "PutItem"
	OpGetItem = "GetItem"
	OpScan    = "Scan"
)

// Request é a requisição já validada, tipada pela operação.
type Request struct {
	Op       string
	Write    port.WriteRequest // PutItem
	Table    domain.LogicalTable
	Key      []byte // GetItem: chave canônica
	Limit    int    // Scan
	StartKey []byte // Scan: ExclusiveStartKey canônica
}

// Parser valida a requisição INTEIRA antes de qualquer caso de uso (SG-07): o que o
// DynamoDB recusaria é recusado aqui, com 400, e nada chega ao Raft.
type Parser struct {
	Tables  domain.TableMap
	Catalog port.Catalog
}

var (
	putForbidden  = []string{"ConditionExpression", "Expected", "ConditionalOperator", "ExpressionAttributeNames", "ExpressionAttributeValues"}
	getForbidden  = []string{"ProjectionExpression", "AttributesToGet", "ExpressionAttributeNames"}
	scanForbidden = []string{"FilterExpression", "ScanFilter", "Segment", "TotalSegments", "ProjectionExpression", "AttributesToGet", "ExpressionAttributeNames", "ExpressionAttributeValues", "ConditionalOperator"}
)

func validation(msg string) error {
	return &APIError{Status: 400, Type: "ValidationException", Message: msg}
}

// Parse lê o corpo (copiando tudo o que guarda: o buffer é do fasthttp, N-06) e valida.
func (p *Parser) Parse(target string, body []byte) (Request, error) {
	op, ok := strings.CutPrefix(target, targetPrefix)
	if !ok {
		return Request{}, &APIError{Status: 400, Type: "UnknownOperationException", Message: "operação desconhecida"}
	}
	switch op {
	case OpPutItem, OpGetItem, OpScan:
	case "DeleteItem", "BatchWriteItem":
		return Request{}, validation("operação não suportada nesta fase (fase 2)")
	default:
		return Request{}, validation("operação fora do escopo do DataReplication")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return Request{}, &APIError{Status: 400, Type: "SerializationException", Message: "corpo JSON inválido"}
	}
	var tableName string
	if err := json.Unmarshal(raw["TableName"], &tableName); err != nil || tableName == "" {
		return Request{}, validation("TableName é obrigatório")
	}
	table, ok := p.Tables.Logical(tableName)
	if !ok {
		return Request{}, &APIError{Status: 400, Type: "ResourceNotFoundException", Message: "Requested resource not found"}
	}
	ks, ok := p.Catalog.KeySchema(table)
	if !ok {
		return Request{}, &APIError{Status: 400, Type: "ResourceNotFoundException", Message: "Requested resource not found"}
	}

	switch op {
	case OpPutItem:
		return p.parsePut(raw, table, ks)
	case OpGetItem:
		return p.parseGet(raw, table, ks)
	default:
		return p.parseScan(raw, table, ks)
	}
}

func forbidden(raw map[string]json.RawMessage, names []string) error {
	for _, n := range names {
		if v, ok := raw[n]; ok && string(v) != "null" {
			return validation("parâmetro fora do escopo: " + n)
		}
	}
	return nil
}

func (p *Parser) parsePut(raw map[string]json.RawMessage, table domain.LogicalTable, ks domain.KeySchema) (Request, error) {
	if err := forbidden(raw, putForbidden); err != nil {
		return Request{}, err
	}
	if rv, ok := raw["ReturnValues"]; ok {
		var s string
		if err := json.Unmarshal(rv, &s); err != nil || (s != "" && s != "NONE") {
			return Request{}, validation("parâmetro fora do escopo: ReturnValues")
		}
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw["Item"], &item); err != nil || item == nil {
		return Request{}, validation("Item é obrigatório")
	}
	if _, ok := item["_dr_v"]; ok {
		return Request{}, validation("o atributo _dr_v é reservado")
	}
	if err := checkKey(item, ks, false); err != nil {
		return Request{}, err
	}
	size, err := itemSize(item)
	if err != nil {
		return Request{}, validation("One or more parameter values were invalid: " + err.Error())
	}
	if size > maxItemSize {
		return Request{}, validation("Item size has exceeded the maximum allowed size")
	}
	key, err := domain.CanonicalKey(ks, item)
	if err != nil {
		return Request{}, validation(err.Error())
	}
	itemJSON, err := json.Marshal(item) // cópia compacta, independente do buffer
	if err != nil {
		return Request{}, validation("Item inválido")
	}
	return Request{Op: OpPutItem, Table: table, Write: port.WriteRequest{
		Ops: []domain.Op{{Table: table, Kind: domain.OpPut, Key: key, Item: itemJSON}},
	}}, nil
}

func (p *Parser) parseGet(raw map[string]json.RawMessage, table domain.LogicalTable, ks domain.KeySchema) (Request, error) {
	if err := forbidden(raw, getForbidden); err != nil {
		return Request{}, err
	}
	var key map[string]json.RawMessage
	if err := json.Unmarshal(raw["Key"], &key); err != nil || key == nil {
		return Request{}, validation("Key é obrigatório")
	}
	if err := checkKey(key, ks, true); err != nil {
		return Request{}, err
	}
	k, err := domain.CanonicalKey(ks, key)
	if err != nil {
		return Request{}, validation(err.Error())
	}
	// ConsistentRead é ignorado: toda leitura é forte (ADR-057).
	return Request{Op: OpGetItem, Table: table, Key: k}, nil
}

func (p *Parser) parseScan(raw map[string]json.RawMessage, table domain.LogicalTable, ks domain.KeySchema) (Request, error) {
	if v, ok := raw["IndexName"]; ok {
		var name string
		_ = json.Unmarshal(v, &name)
		if err := ValidateIndex(name); err != nil {
			return Request{}, err
		}
	}
	if err := forbidden(raw, scanForbidden); err != nil {
		return Request{}, err
	}
	if v, ok := raw["Select"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil || (s != "" && s != "ALL_ATTRIBUTES") {
			return Request{}, validation("parâmetro fora do escopo: Select")
		}
	}
	req := Request{Op: OpScan, Table: table}
	if v, ok := raw["Limit"]; ok {
		if err := json.Unmarshal(v, &req.Limit); err != nil || req.Limit < 1 {
			return Request{}, validation("Limit precisa ser maior ou igual a 1")
		}
	}
	if v, ok := raw["ExclusiveStartKey"]; ok && string(v) != "null" {
		var key map[string]json.RawMessage
		if err := json.Unmarshal(v, &key); err != nil {
			return Request{}, validation("ExclusiveStartKey inválida")
		}
		if err := checkKey(key, ks, true); err != nil {
			return Request{}, err
		}
		k, err := domain.CanonicalKey(ks, key)
		if err != nil {
			return Request{}, validation(err.Error())
		}
		req.StartKey = k
	}
	return req, nil
}

// checkKey confere os atributos da chave: presentes e com o tipo do KeySchema. exact
// exige que só eles estejam presentes (GetItem, ExclusiveStartKey).
func checkKey(attrs map[string]json.RawMessage, ks domain.KeySchema, exact bool) error {
	for _, ka := range []domain.KeyAttr{ks.Partition, ks.Sort} {
		if ka.Name == "" {
			continue
		}
		v, ok := attrs[ka.Name]
		if !ok {
			if exact {
				return validation("The provided key element does not match the schema")
			}
			return validation("One or more parameter values were invalid: Missing the key " + ka.Name + " in the item")
		}
		typ, _, err := validateValue(v)
		if err != nil {
			return validation("One or more parameter values were invalid: " + err.Error())
		}
		if typ != ka.Type {
			return validation("One or more parameter values were invalid: Type mismatch for key " + ka.Name)
		}
	}
	if exact && len(attrs) != len(ks.Names()) {
		return validation("The provided key element does not match the schema")
	}
	return nil
}

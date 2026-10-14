package dynamohttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

type fakeCatalog map[domain.LogicalTable]domain.KeySchema

func (f fakeCatalog) KeySchema(t domain.LogicalTable) (domain.KeySchema, bool) {
	ks, ok := f[t]
	return ks, ok
}
func (fakeCatalog) Ready() bool { return true }

func newParser(t *testing.T, cell string) *Parser {
	t.Helper()
	tm, err := domain.NewTableMap(cell, []string{"cadastro_cliente_cell_{cell}", "t1_cell_{cell}"})
	if err != nil {
		t.Fatal(err)
	}
	pk := domain.KeySchema{Partition: domain.KeyAttr{Name: "pk", Type: "S"}}
	return &Parser{Tables: tm, Catalog: fakeCatalog{"cadastro_cliente_cell_{cell}": pk, "t1_cell_{cell}": pk}}
}

// panicWriter falha o teste se qualquer escrita chegar ao caso de uso: nada é proposto.
type panicWriter struct{ t *testing.T }

func (w panicWriter) Write(context.Context, port.WriteRequest) (port.WriteResult, error) {
	w.t.Fatal("uma requisição recusada chegou ao Writer")
	return port.WriteResult{}, nil
}

func call(s *Server, op, body string) *fasthttp.RequestCtx {
	var c fasthttp.RequestCtx
	c.Request.Header.Set("X-Amz-Target", "DynamoDB_20120810."+op)
	c.Request.SetBodyString(body)
	s.handle(&c)
	return &c
}

// CA-10, CA-13 e R.3: cada recusa sai com 400 e o tipo certo, sem chegar ao Writer.
func TestParserRefusals(t *testing.T) {
	p := newParser(t, "2")
	s := New(panicWriter{t}, nil, p.Parse, Options{Deadline: Deadline(time.Second), IdleTimeout: 120 * time.Second})
	item := `"nome":{"S":"Maria"}`
	cases := []struct {
		name, op, body, typ, msg string
	}{
		{"_dr_v", "PutItem", `{"TableName":"cadastro_cliente_cell_2","Item":{"pk":{"S":"a"},"_dr_v":{"N":"1"}}}`, "ValidationException", "o atributo _dr_v é reservado"},
		{"sem pk", "PutItem", `{"TableName":"cadastro_cliente_cell_2","Item":{` + item + `}}`, "ValidationException", "Missing the key pk in the item"},
		{"pk numérica", "PutItem", `{"TableName":"cadastro_cliente_cell_2","Item":{"pk":{"N":"1"},` + item + `}}`, "ValidationException", "Type mismatch for key pk"},
		{"tabela de outra célula", "PutItem", `{"TableName":"cadastro_cliente_cell_1","Item":{"pk":{"S":"a"}}}`, "ResourceNotFoundException", "Requested resource not found"},
		{"condicional", "PutItem", `{"TableName":"cadastro_cliente_cell_2","Item":{"pk":{"S":"a"}},"ConditionExpression":"attribute_not_exists(pk)"}`, "ValidationException", "parâmetro fora do escopo: ConditionExpression"},
		{"ReturnValues", "PutItem", `{"TableName":"cadastro_cliente_cell_2","Item":{"pk":{"S":"a"}},"ReturnValues":"ALL_OLD"}`, "ValidationException", "parâmetro fora do escopo: ReturnValues"},
		{"N inválido", "PutItem", `{"TableName":"cadastro_cliente_cell_2","Item":{"pk":{"S":"a"},"x":{"N":"abc"}}}`, "ValidationException", "valor N inválido"},
		{"tipo duplo", "PutItem", `{"TableName":"cadastro_cliente_cell_2","Item":{"pk":{"S":"a"},"x":{"S":"a","N":"1"}}}`, "ValidationException", "exatamente um tipo"},
		{"item grande", "PutItem", `{"TableName":"cadastro_cliente_cell_2","Item":{"pk":{"S":"a"},"x":{"S":"` + strings.Repeat("a", 401*1024) + `"}}}`, "ValidationException", "Item size has exceeded the maximum allowed size"},
		{"delete fase 2", "DeleteItem", `{}`, "ValidationException", "operação não suportada nesta fase (fase 2)"},
		{"batch fase 2", "BatchWriteItem", `{}`, "ValidationException", "operação não suportada nesta fase (fase 2)"},
		{"update fora", "UpdateItem", `{}`, "ValidationException", "operação fora do escopo do DataReplication"},
		{"query fora", "Query", `{}`, "ValidationException", "operação fora do escopo do DataReplication"},
		{"get com projeção", "GetItem", `{"TableName":"cadastro_cliente_cell_2","Key":{"pk":{"S":"a"}},"ProjectionExpression":"nome"}`, "ValidationException", "parâmetro fora do escopo: ProjectionExpression"},
		{"get chave a mais", "GetItem", `{"TableName":"cadastro_cliente_cell_2","Key":{"pk":{"S":"a"},"x":{"S":"b"}}}`, "ValidationException", "does not match the schema"},
		{"scan em índice", "Scan", `{"TableName":"cadastro_cliente_cell_2","IndexName":"gsi1"}`, "ValidationException", "global secondary indexes"},
		{"scan com filtro", "Scan", `{"TableName":"cadastro_cliente_cell_2","FilterExpression":"x = :v"}`, "ValidationException", "parâmetro fora do escopo: FilterExpression"},
		{"scan segmentado", "Scan", `{"TableName":"cadastro_cliente_cell_2","Segment":0,"TotalSegments":2}`, "ValidationException", "parâmetro fora do escopo: Segment"},
		{"scan limite zero", "Scan", `{"TableName":"cadastro_cliente_cell_2","Limit":0}`, "ValidationException", "Limit"},
	}
	for _, tc := range cases {
		c := call(s, tc.op, tc.body)
		if c.Response.StatusCode() != 400 {
			t.Errorf("%s: status %d", tc.name, c.Response.StatusCode())
			continue
		}
		b := decode(t, c)
		if b.Type != "com.amazonaws.dynamodb.v20120810#"+tc.typ || !strings.Contains(b.Message, tc.msg) {
			t.Errorf("%s: %+v", tc.name, b)
		}
	}
}

// SG-08: "t1_cell_1" na célula 1 resolve pela lista permitida; a chave e o item saem
// canônicos e independentes do buffer.
func TestParsePutItem(t *testing.T) {
	p := newParser(t, "1")
	body := []byte(`{"TableName":"t1_cell_1","Item":{"pk":{"S":"cli-1"},"nome":{"S":"Maria"}}}`)
	req, err := p.Parse("DynamoDB_20120810.PutItem", body)
	if err != nil {
		t.Fatal(err)
	}
	for i := range body {
		body[i] = 'x' // o fasthttp reutiliza o buffer (N-06)
	}
	op := req.Write.Ops[0]
	if req.Op != OpPutItem || op.Table != "t1_cell_{cell}" || string(op.Key) != `{"pk":{"S":"cli-1"}}` {
		t.Fatalf("requisição = %+v / %s", req, op.Key)
	}
	var item map[string]any
	if err := json.Unmarshal(op.Item, &item); err != nil || len(item) != 2 {
		t.Fatalf("item = %s %v", op.Item, err)
	}
}

type fakeReader struct {
	item []byte
	page port.ScanPage
	err  error
}

func (f fakeReader) Get(context.Context, domain.LogicalTable, []byte) ([]byte, error) {
	return f.item, f.err
}
func (f fakeReader) Scan(context.Context, domain.LogicalTable, int, []byte) (port.ScanPage, error) {
	return f.page, f.err
}

func TestReadResponsesAndIndexHeader(t *testing.T) {
	p := newParser(t, "1")
	r := fakeReader{
		item: []byte(`{"pk":{"S":"a"},"nome":{"S":"Maria"}}`),
		page: port.ScanPage{Items: [][]byte{[]byte(`{"pk":{"S":"a"}}`)}, Scanned: 1, LastKey: []byte(`{"pk":{"S":"a"}}`)},
	}
	s := New(fakeWriter{res: port.WriteResult{Index: 17}}, r, p.Parse, Options{Deadline: Deadline(time.Second), IdleTimeout: 120 * time.Second})

	c := call(s, "PutItem", `{"TableName":"cadastro_cliente_cell_1","Item":{"pk":{"S":"a"}}}`)
	if c.Response.StatusCode() != 200 || string(c.Response.Header.Peek("x-dr-index")) != "17" {
		t.Fatalf("PutItem: %d x-dr-index=%q", c.Response.StatusCode(), c.Response.Header.Peek("x-dr-index"))
	}
	c = call(s, "GetItem", `{"TableName":"cadastro_cliente_cell_1","Key":{"pk":{"S":"a"}},"ConsistentRead":false}`)
	if c.Response.StatusCode() != 200 || string(c.Response.Body()) != `{"Item":{"pk":{"S":"a"},"nome":{"S":"Maria"}}}` {
		t.Fatalf("GetItem: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	c = call(s, "Scan", `{"TableName":"cadastro_cliente_cell_1","Limit":1}`)
	if string(c.Response.Body()) != `{"Items":[{"pk":{"S":"a"}}],"Count":1,"ScannedCount":1,"LastEvaluatedKey":{"pk":{"S":"a"}}}` {
		t.Fatalf("Scan: %s", c.Response.Body())
	}

	r.item = nil
	s = New(fakeWriter{}, r, p.Parse, Options{Deadline: Deadline(time.Second), IdleTimeout: 120 * time.Second})
	if c = call(s, "GetItem", `{"TableName":"cadastro_cliente_cell_1","Key":{"pk":{"S":"x"}}}`); string(c.Response.Body()) != `{}` {
		t.Fatalf("GetItem sem item: %s", c.Response.Body())
	}
	for err, code := range map[error]string{usecase.ErrReadTimeout: "READ_TIMEOUT", usecase.ErrGateNotReady: "GATE_NOT_READY", usecase.ErrNoLeader: "NO_LEADER"} {
		s = New(fakeWriter{}, fakeReader{err: err}, p.Parse, Options{Deadline: Deadline(time.Second), IdleTimeout: 120 * time.Second})
		c = call(s, "GetItem", `{"TableName":"cadastro_cliente_cell_1","Key":{"pk":{"S":"x"}}}`)
		if c.Response.StatusCode() != 503 || string(c.Response.Header.Peek("x-dr-code")) != code {
			t.Fatalf("%v: %d %q", err, c.Response.StatusCode(), c.Response.Header.Peek("x-dr-code"))
		}
	}
}

// O-16: cada requisição conta o resultado certo.
func TestHandlerRecordsMetrics(t *testing.T) {
	p := newParser(t, "1")
	m := &recMetrics{}
	o := Options{Deadline: Deadline(time.Second), IdleTimeout: 120 * time.Second, Metrics: m}
	put := `{"TableName":"cadastro_cliente_cell_1","Item":{"pk":{"S":"a"}}}`
	call(New(fakeWriter{res: port.WriteResult{Index: 1}}, fakeReader{}, p.Parse, o), "PutItem", put)
	call(New(fakeWriter{err: usecase.ErrNoLeader}, fakeReader{}, p.Parse, o), "PutItem", put)
	call(New(panicWriter{t}, fakeReader{}, p.Parse, o), "PutItem", `{"TableName":"cadastro_cliente_cell_1","Item":{"pk":{"S":"a"},"_dr_v":{"N":"1"}}}`)
	call(New(fakeWriter{}, fakeReader{item: []byte(`{}`)}, p.Parse, o), "GetItem", `{"TableName":"cadastro_cliente_cell_1","Key":{"pk":{"S":"a"}}}`)
	call(New(fakeWriter{}, fakeReader{err: usecase.ErrReadTimeout}, p.Parse, o), "Scan", `{"TableName":"cadastro_cliente_cell_1"}`)
	call(New(fakeWriter{}, fakeReader{}, p.Parse, o), "UpdateItem", `{}`) // fora do escopo: não conta
	for k, want := range map[string]int{"write:ok": 1, "write:no_leader": 1, "write:recusada": 1, "read:get:ok": 1, "read:scan:read_timeout": 1} {
		if got := m.n(k); got != want {
			t.Errorf("%s = %d, quer %d", k, got, want)
		}
	}
	m.mu.Lock()
	total := 0
	for _, v := range m.calls {
		total += v
	}
	m.mu.Unlock()
	if total != 5 {
		t.Errorf("total de eventos = %d, quer 5 (UpdateItem não conta)", total)
	}
}

package dynamohttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/valyala/fasthttp"

	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

type fakeWriter struct {
	res port.WriteResult
	err error
}

func (f fakeWriter) Write(context.Context, port.WriteRequest) (port.WriteResult, error) {
	return f.res, f.err
}

func okParse(string, []byte) (Request, error) { return Request{Op: OpPutItem}, nil }

func newServer(w port.Writer, parse ParseFunc) *Server {
	return New(w, nil, parse, Options{Deadline: Deadline(7500 * time.Millisecond), IdleTimeout: 120 * time.Second})
}

func do(s *Server) *fasthttp.RequestCtx {
	var c fasthttp.RequestCtx
	c.Request.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")
	c.Request.SetBodyString(`{}`)
	s.handle(&c)
	return &c
}

func decode(t *testing.T, c *fasthttp.RequestCtx) errBody {
	t.Helper()
	var b errBody
	if err := json.Unmarshal(c.Response.Body(), &b); err != nil {
		t.Fatalf("corpo não é JSON válido: %v (%q)", err, c.Response.Body())
	}
	return b
}

// O WriteTimeout não pode ser menor que o prazo da requisição (antes: 5 s contra 7,5 s).
func TestTimeoutsAreCoherent(t *testing.T) {
	s := newServer(fakeWriter{}, okParse)
	if s.srv.WriteTimeout < s.deadline {
		t.Fatalf("WriteTimeout %v menor que REQUEST_DEADLINE %v", s.srv.WriteTimeout, s.deadline)
	}
	if s.srv.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout não veio da configuração: %v", s.srv.IdleTimeout)
	}
}

// Mensagens com aspas, barras e quebras de linha continuam sendo JSON válido.
func TestErrorBodyIsAlwaysValidJSON(t *testing.T) {
	nasty := "a \"b\" \\ c\nd\t</script>   é"
	s := newServer(fakeWriter{}, func(string, []byte) (Request, error) {
		return Request{}, errors.New(nasty)
	})
	c := do(s)
	if c.Response.StatusCode() != 400 {
		t.Fatalf("status %d", c.Response.StatusCode())
	}
	b := decode(t, c)
	if b.Message != nasty || b.Type != "com.amazonaws.dynamodb.v20120810#ValidationException" {
		t.Fatalf("corpo inesperado: %+v", b)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"pending", usecase.ErrPending, 503, "PENDING"},
		{"deadline", fmt.Errorf("propose: %w", context.DeadlineExceeded), 503, "PENDING"},
		{"noleader", usecase.ErrNoLeader, 503, "NO_LEADER"},
		{"applier", usecase.ErrApplierStopped, 503, "APPLIER_STOPPED"},
		{"backpressure", fmt.Errorf("propose: %w", usecase.ErrBackpressure), 400, ""},
		{"outro", errors.New("boom"), 500, ""},
	}
	for _, tc := range cases {
		c := do(newServer(fakeWriter{err: tc.err}, okParse))
		if c.Response.StatusCode() != tc.status {
			t.Errorf("%s: status %d", tc.name, c.Response.StatusCode())
		}
		decode(t, c)
		if got := string(c.Response.Header.Peek("x-dr-code")); got != tc.code {
			t.Errorf("%s: x-dr-code %q", tc.name, got)
		}
	}
	if got := do(newServer(fakeWriter{err: usecase.ErrNoLeader}, okParse)).Response.Header.Peek("Retry-After"); string(got) != "1" {
		t.Errorf("NO_LEADER deve levar Retry-After, veio %q", got)
	}
}

// ADR-050: toda Query em GSI vira ValidationException 400 via erro tipado do parser.
func TestQueryOnGSIRefused(t *testing.T) {
	if err := ValidateIndex(""); err != nil {
		t.Fatalf("tabela base deve ser aceita: %v", err)
	}
	if err := ValidateIndex("gsi1"); err == nil {
		t.Fatal("Query em GSI deve ser recusada, com ou sem ConsistentRead")
	}
	s := newServer(fakeWriter{}, func(string, []byte) (Request, error) {
		return Request{}, ValidateIndex("gsi1")
	})
	c := do(s)
	if c.Response.StatusCode() != 400 {
		t.Fatalf("status %d", c.Response.StatusCode())
	}
	if b := decode(t, c); b.Type != "com.amazonaws.dynamodb.v20120810#ValidationException" {
		t.Fatalf("tipo %q", b.Type)
	}
}

// Sem estado compartilhado no handler: muitas requisições em paralelo, cada uma com a
// sua resposta, sem corrida.
func TestHandlerParallel(t *testing.T) {
	s := newServer(fakeWriter{res: port.WriteResult{Index: 3}}, okParse)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c := do(s); c.Response.StatusCode() != 200 || string(c.Response.Header.Peek("x-dr-index")) != "3" {
				t.Error("resposta inesperada")
			}
		}()
	}
	wg.Wait()
}

// Buffer cheio vira ThrottlingException, que os SDKs repetem com backoff (ADR-056).
func TestBackpressureIsThrottling(t *testing.T) {
	c := do(newServer(fakeWriter{err: usecase.ErrBackpressure}, okParse))
	var body struct {
		Type string `json:"__type"`
	}
	if err := json.Unmarshal(c.Response.Body(), &body); err != nil {
		t.Fatal(err)
	}
	if c.Response.StatusCode() != 400 || body.Type != "com.amazonaws.dynamodb.v20120810#ThrottlingException" {
		t.Fatalf("status %d tipo %q", c.Response.StatusCode(), body.Type)
	}
}

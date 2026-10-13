// Package dynamohttp é o adaptador de entrada: HTTP compatível com DynamoDB.
// Só traduz protocolo; nenhuma regra de negócio mora aqui.
package dynamohttp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/valyala/fasthttp"

	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// Deadline é o prazo de toda requisição; vem da configuração e é menor que o timeout do SDK.
type Deadline time.Duration

// Options reúne os prazos do servidor, todos vindos da configuração.
type Options struct {
	Deadline    Deadline      // REQUEST_DEADLINE (7,5 s)
	IdleTimeout time.Duration // IDLE_TIMEOUT: maior que o idle dos SDKs (Java 60 s, Go 90 s)
}

// writeMargin é a folga entre o fim do prazo da requisição e o fim da escrita da
// resposta. WriteTimeout = REQUEST_DEADLINE + folga: a resposta (inclusive o 503 de
// prazo estourado) ainda precisa caber depois de o contexto expirar.
const writeMargin = 2 * time.Second

type Server struct {
	deadline time.Duration
	srv      *fasthttp.Server
	writer   port.Writer
	parse    func(target string, body []byte) (port.WriteRequest, error)
}

func New(w port.Writer, parse func(string, []byte) (port.WriteRequest, error), o Options) *Server {
	s := &Server{writer: w, parse: parse, deadline: time.Duration(o.Deadline)}
	s.srv = &fasthttp.Server{
		Handler:               s.handle,
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          s.deadline + writeMargin, // coerente com REQUEST_DEADLINE: nunca corta a resposta antes do prazo
		IdleTimeout:           o.IdleTimeout,            // maior que o idle dos SDKs, senão o SDK reusa conexão já fechada
		MaxRequestBodySize:    4 << 20,                  // 4 MiB (decisão de entrada máxima)
		NoDefaultServerHeader: true,
		ReduceMemoryUsage:     false, // preferir velocidade; medir no spike
	}
	return s
}

// APIError é o erro de protocolo que o parser (ou qualquer validação de borda) devolve
// para escolher o status e o tipo de exceção DynamoDB.
type APIError struct {
	Status  int
	Type    string // ex.: ValidationException
	Message string
}

func (e *APIError) Error() string { return e.Type + ": " + e.Message }

// ValidateConsistentRead aplica o ADR-050: ConsistentRead=true em índice secundário
// global é recusado com a mesma ValidationException do DynamoDB. O parser de Query/Scan
// chama esta função assim que lê IndexName e ConsistentRead. Na tabela base vale
// READ_MODE_DEFAULT; em GSI a leitura é sempre eventual (header x-dr-consistency).
func ValidateConsistentRead(indexName string, consistentRead bool) error {
	if indexName != "" && consistentRead {
		return &APIError{Status: 400, Type: "ValidationException", Message: "Consistent reads are not supported on global secondary indexes"}
	}
	return nil
}

func (s *Server) handle(c *fasthttp.RequestCtx) {
	target := string(c.Request.Header.Peek("X-Amz-Target")) // ex.: DynamoDB_20120810.PutItem
	// ATENÇÃO: o body pertence ao fasthttp e é reutilizado depois do handler.
	// O parser deve copiar tudo o que for ao log antes de retornar.
	req, err := s.parse(target, c.PostBody())
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			writeErr(c, ae.Status, ae.Type, ae.Message)
		} else {
			writeErr(c, 400, "ValidationException", err.Error())
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.deadline)
	defer cancel()
	res, err := s.writer.Write(ctx, req)
	switch {
	case errors.Is(err, usecase.ErrLeaseExpired):
		c.Response.Header.Set("x-dr-mode", "degraded") // ainda degradado, mas sem escrever (ADR-044)
		writeCoded(c, "LEASE_EXPIRED", "degraded-mode lease expired: writes are refused until a human renews it")
		return
	case errors.Is(err, usecase.ErrNoLeader):
		c.Response.Header.Set("Retry-After", strconv.Itoa(1))
		writeCoded(c, "NO_LEADER", "no leader known, retry")
		return
	case errors.Is(err, usecase.ErrPending), errors.Is(err, context.DeadlineExceeded):
		writeCoded(c, "PENDING", "result unknown, retry") // o SDK retenta; dedupe e versão absorvem
		return
	case err != nil:
		writeErr(c, 500, "InternalServerError", "internal error")
		return
	}
	if res.Degraded {
		c.Response.Header.Set("x-dr-mode", "degraded") // D10
	}
	c.SetContentType("application/x-amz-json-1.0")
	c.SetBodyString("{}")
}

// writeCoded responde 503 ServiceUnavailable com um código da camada no header x-dr-code.
func writeCoded(c *fasthttp.RequestCtx, code, msg string) {
	c.Response.Header.Set("x-dr-code", code)
	writeErr(c, 503, "ServiceUnavailable", code+": "+msg)
}

type errBody struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// writeErr serializa com encoding/json: mensagens com aspas, barras ou quebras de linha
// continuam sendo JSON válido (nada de concatenação).
func writeErr(c *fasthttp.RequestCtx, status int, typ, msg string) {
	b, err := json.Marshal(errBody{Type: "com.amazonaws.dynamodb.v20120810#" + typ, Message: msg})
	if err != nil { // inalcançável com strings; falha fechada
		b = []byte(`{"__type":"com.amazonaws.dynamodb.v20120810#InternalServerError","message":"internal error"}`)
		status = 500
	}
	c.SetStatusCode(status)
	c.SetContentType("application/x-amz-json-1.0")
	c.SetBody(b)
}

func (s *Server) ListenAndServe(addr string) error { return s.srv.ListenAndServe(addr) }
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.ShutdownWithContext(ctx)
}

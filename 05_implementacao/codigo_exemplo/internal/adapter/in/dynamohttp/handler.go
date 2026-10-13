// Package dynamohttp é o adaptador de entrada: HTTP compatível com DynamoDB.
// Só traduz protocolo; nenhuma regra de negócio mora aqui.
package dynamohttp

import (
	"context"
	"errors"
	"time"

	"github.com/valyala/fasthttp"

	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// Deadline é o prazo de toda requisição; vem da configuração e é menor que o timeout do SDK.
type Deadline time.Duration

type Server struct {
	deadline time.Duration
	srv      *fasthttp.Server
	writer   port.Writer
	parse    func(target string, body []byte) (port.WriteRequest, error)
}

func New(w port.Writer, parse func(string, []byte) (port.WriteRequest, error), d Deadline) *Server {
	s := &Server{writer: w, parse: parse, deadline: time.Duration(d)}
	s.srv = &fasthttp.Server{
		Handler:               s.handle,
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          5 * time.Second,
		IdleTimeout:           120 * time.Second, // maior que o idle dos SDKs (Java 60s, Go 90s), senão o SDK reusa conexão já fechada
		MaxRequestBodySize:    4 << 20,           // 4 MiB (decisão de entrada máxima)
		NoDefaultServerHeader: true,
		ReduceMemoryUsage:     false, // preferir velocidade; medir no spike
	}
	return s
}

func (s *Server) handle(c *fasthttp.RequestCtx) {
	target := string(c.Request.Header.Peek("X-Amz-Target")) // ex.: DynamoDB_20120810.PutItem
	// ATENÇÃO: o body pertence ao fasthttp e é reutilizado depois do handler.
	// O parser deve copiar tudo o que for ao log antes de retornar.
	req, err := s.parse(target, c.PostBody())
	if err != nil {
		writeErr(c, 400, "ValidationException", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.deadline)
	defer cancel()
	res, err := s.writer.Write(ctx, req)
	switch {
	case errors.Is(err, usecase.ErrPending):
		writeErr(c, 503, "ServiceUnavailable", "retry") // o SDK retenta
		return
	case err != nil:
		writeErr(c, 500, "InternalServerError", "")
		return
	}
	if res.Degraded {
		c.Response.Header.Set("x-dr-mode", "degraded") // D10
	}
	c.SetContentType("application/x-amz-json-1.0")
	c.SetBodyString("{}")
}

func writeErr(c *fasthttp.RequestCtx, status int, typ, msg string) {
	c.SetStatusCode(status)
	c.SetContentType("application/x-amz-json-1.0")
	c.SetBodyString(`{"__type":"com.amazonaws.dynamodb.v20120810#` + typ + `","message":"` + msg + `"}`)
}

func (s *Server) ListenAndServe(addr string) error { return s.srv.ListenAndServe(addr) }
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.ShutdownWithContext(ctx)
}

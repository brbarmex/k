// Package dynamohttp é o adaptador de entrada: HTTP compatível com DynamoDB.
// Só traduz protocolo; nenhuma regra de negócio mora aqui.
package dynamohttp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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
	Metrics     port.Metrics  // ACKs e leituras (O-16); nulo = sem métricas
}

// writeMargin é a folga entre o fim do prazo da requisição e o fim da escrita da
// resposta. WriteTimeout = REQUEST_DEADLINE + folga: a resposta (inclusive o 503 de
// prazo estourado) ainda precisa caber depois de o contexto expirar.
const writeMargin = 2 * time.Second

// ParseFunc valida e tipa a requisição (Parser.Parse em produção).
type ParseFunc func(target string, body []byte) (Request, error)

type Server struct {
	deadline time.Duration
	srv      *fasthttp.Server
	writer   port.Writer
	reader   port.Reader
	parse    ParseFunc
	metrics  port.Metrics
}

func New(w port.Writer, r port.Reader, parse ParseFunc, o Options) *Server {
	s := &Server{writer: w, reader: r, parse: parse, deadline: time.Duration(o.Deadline), metrics: port.MetricsOrNop(o.Metrics)}
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

// ValidateIndex aplica o ADR-050: Query ou Scan em índice secundário global (GSI) é
// recusado com ValidationException, com ou sem ConsistentRead, porque o GSI nunca é
// forte e o DataReplication tem um único modo, strong (ADR-057). O parser de Query/Scan
// chama esta função assim que lê IndexName; isGlobal vem do catálogo de tabelas.
func ValidateIndex(indexName string, isGlobal bool) error {
	if indexName != "" && isGlobal {
		return &APIError{Status: 400, Type: "ValidationException", Message: "Queries on global secondary indexes are not supported: the DataReplication serves only strongly consistent reads"}
	}
	return nil
}

// handle mede toda requisição da porta de API (O-28): em andamento, operação, código HTTP
// e latência, qualquer que seja o resultado. rate(dr_http_requests_total) é o TPS de
// entrada da célula, que o k6 e o Grafana comparam com o que vira ACK.
func (s *Server) handle(c *fasthttp.RequestCtx) {
	start := time.Now()
	s.metrics.HTTPInFlight(1)
	defer func() {
		s.metrics.HTTPInFlight(-1)
		s.metrics.HTTPRequest(httpOp(c.Request.Header.Peek("X-Amz-Target")), c.Response.StatusCode(), time.Since(start))
	}()
	s.serve(c, start)
}

// httpOp é o rótulo `op`: a operação do DynamoDB, ou "outra" fora da lista conhecida
// (o rótulo não pode crescer sem limite com o que o cliente manda).
func httpOp(target []byte) string {
	op, ok := strings.CutPrefix(string(target), targetPrefix)
	if !ok {
		return "outra"
	}
	switch op {
	case OpPutItem, OpGetItem, OpScan, "BatchWriteItem", "DeleteItem", "UpdateItem", "Query",
		"TransactWriteItems", "BatchGetItem", "DescribeTable":
		return op
	}
	return "outra"
}

func (s *Server) serve(c *fasthttp.RequestCtx, start time.Time) {
	target := string(c.Request.Header.Peek("X-Amz-Target")) // ex.: DynamoDB_20120810.PutItem
	// ATENÇÃO: o body pertence ao fasthttp e é reutilizado depois do handler.
	// O parser copia tudo o que guarda antes de retornar (N-06).
	req, err := s.parse(target, c.PostBody())
	if err != nil {
		var ae *APIError
		if !errors.As(err, &ae) { // erro de borda sem tipo: sempre 400
			err = &APIError{Status: 400, Type: "ValidationException", Message: err.Error()}
		}
		s.record(target, err, start)
		writeError(c, err)
		return
	}
	defer func() { s.record(target, err, start) }()
	ctx, cancel := context.WithTimeout(context.Background(), s.deadline)
	defer cancel()
	switch req.Op {
	case OpGetItem:
		var item []byte
		item, err = s.reader.Get(ctx, req.Table, req.Key)
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, getResponse{Item: item})
	case OpScan:
		var page port.ScanPage
		page, err = s.reader.Scan(ctx, req.Table, req.Limit, req.StartKey)
		if err != nil {
			writeError(c, err)
			return
		}
		items := make([]json.RawMessage, len(page.Items))
		for i, it := range page.Items {
			items[i] = it
		}
		writeJSON(c, scanResponse{Items: items, Count: len(items), ScannedCount: page.Scanned, LastEvaluatedKey: page.LastKey})
	default:
		var res port.WriteResult
		res, err = s.writer.Write(ctx, req.Write)
		if err != nil {
			writeError(c, err)
			return
		}
		if res.Degraded {
			c.Response.Header.Set("x-dr-mode", "degraded") // D10
		}
		c.Response.Header.Set("x-dr-index", strconv.FormatUint(uint64(res.Index), 10)) // extensão da POC (N-05)
		c.SetContentType("application/x-amz-json-1.0")
		c.SetBodyString("{}")
	}
}

type getResponse struct {
	Item json.RawMessage `json:"Item,omitempty"`
}

type scanResponse struct {
	Items            []json.RawMessage `json:"Items"`
	Count            int               `json:"Count"`
	ScannedCount     int               `json:"ScannedCount"`
	LastEvaluatedKey json.RawMessage   `json:"LastEvaluatedKey,omitempty"`
}

func writeJSON(c *fasthttp.RequestCtx, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeErr(c, 500, "InternalServerError", "internal error")
		return
	}
	c.SetContentType("application/x-amz-json-1.0")
	c.SetBody(b)
}

// record conta o resultado da requisição pelo alvo (O-16). Operação desconhecida não conta.
func (s *Server) record(target string, err error, start time.Time) {
	switch target {
	case targetPrefix + OpPutItem:
		s.metrics.WriteDone(resultOf(err), time.Since(start))
	case targetPrefix + OpGetItem:
		s.metrics.ReadDone("get", resultOf(err))
	case targetPrefix + OpScan:
		s.metrics.ReadDone("scan", resultOf(err))
	}
}

// resultOf é o rótulo `resultado` das métricas, na mesma classificação do writeError.
func resultOf(err error) string {
	var ae *APIError
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &ae):
		if ae.Type == "ThrottlingException" {
			return "throttling"
		}
		return "recusada"
	case errors.Is(err, usecase.ErrLeaseExpired):
		return "lease_expired"
	case errors.Is(err, usecase.ErrNoLeader):
		return "no_leader"
	case errors.Is(err, usecase.ErrBackpressure):
		return "throttling"
	case errors.Is(err, usecase.ErrGateNotReady):
		return "gate_not_ready"
	case errors.Is(err, usecase.ErrReadTimeout):
		return "read_timeout"
	case errors.Is(err, usecase.ErrApplierStopped):
		return "applier_stopped"
	case errors.Is(err, usecase.ErrPending), errors.Is(err, context.DeadlineExceeded):
		return "pending"
	default:
		return "erro"
	}
}

// writeError é o ÚNICO mapeamento de erro para HTTP.
func writeError(c *fasthttp.RequestCtx, err error) {
	var ae *APIError
	switch {
	case errors.As(err, &ae):
		writeErr(c, ae.Status, ae.Type, ae.Message)
	case errors.Is(err, usecase.ErrLeaseExpired):
		c.Response.Header.Set("x-dr-mode", "degraded") // ainda degradado, mas sem escrever (ADR-044)
		writeCoded(c, "LEASE_EXPIRED", "degraded-mode lease expired: writes are refused until a human renews it")
	case errors.Is(err, usecase.ErrNoLeader):
		c.Response.Header.Set("Retry-After", strconv.Itoa(1))
		writeCoded(c, "NO_LEADER", "no leader known, retry")
	case errors.Is(err, usecase.ErrBackpressure):
		// 400 ThrottlingException: os SDKs repetem com backoff; nada entrou no log (ADR-056).
		writeErr(c, 400, "ThrottlingException", "rate of requests exceeds the allowed throughput")
	case errors.Is(err, usecase.ErrGateNotReady):
		writeCoded(c, "GATE_NOT_READY", "gate not ready on the new leader, retry")
	case errors.Is(err, usecase.ErrReadTimeout):
		writeCoded(c, "READ_TIMEOUT", "strong read barrier not reached in time, retry")
	case errors.Is(err, usecase.ErrApplierStopped):
		writeCoded(c, "APPLIER_STOPPED", "the applier on this cell is stopped on a poison item; human action is required (ADR-038)")
	case errors.Is(err, usecase.ErrPending), errors.Is(err, context.DeadlineExceeded):
		writeCoded(c, "PENDING", "result unknown, retry") // o SDK retenta; a versão absorve
	default:
		writeErr(c, 500, "InternalServerError", "internal error")
	}
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

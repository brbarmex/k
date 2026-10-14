// Package adminhttp é o endpoint de administração (S.7) com net/http: /livez, /readyz e
// /status.
package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"example.com/datareplication/internal/usecase"
)

// Reporter é a fonte do estado da célula (usecase.StatusReporter).
type Reporter interface {
	Status() usecase.CellStatus
	Ready() (bool, string)
}

// Server é o servidor de administração.
type Server struct {
	srv *http.Server
}

// New monta as rotas no endereço addr.
func New(addr string, r Reporter) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ok, why := r.Ready(); !ok {
			http.Error(w, why, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Status())
	})
	return &Server{srv: &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}}
}

// Handler expõe as rotas (testes).
func (s *Server) Handler() http.Handler { return s.srv.Handler }

func (s *Server) ListenAndServe() error {
	if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

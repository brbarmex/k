// Package adminhttp é o endpoint de administração (S.7) com net/http: /livez, /readyz,
// /status e POST /admin/reconstruir (AP-18). Só na porta de administração, nunca publicada
// além do host.
package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"example.com/datareplication/internal/usecase"
)

// Reporter é a fonte do estado da célula (usecase.StatusReporter).
type Reporter interface {
	Status() usecase.CellStatus
	Ready() (bool, string)
}

// Rebuilder dispara a reconstrução manual (usecase.Rebuild).
type Rebuilder interface {
	Trigger() error
}

// Server é o servidor de administração.
type Server struct {
	srv *http.Server
}

// New monta as rotas no endereço addr. metrics é o handler de /metrics (O-17); nulo não
// registra a rota. O pacote não importa o Prometheus. rb liga o POST /admin/reconstruir;
// nulo não registra a rota.
func New(addr string, r Reporter, metrics http.Handler, rb Rebuilder) *Server {
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
	if metrics != nil {
		mux.Handle("GET /metrics", metrics)
	}
	if rb != nil {
		mux.HandleFunc("POST /admin/reconstruir", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := rb.Trigger(); errors.Is(err, usecase.ErrLeaderRebuild) {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"erro": "a líder não se reconstrói"})
				return
			} else if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"erro": err.Error()})
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"estado": "reconstruindo"})
		})
	}
	return &Server{srv: &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}}
}

func (s *Server) ListenAndServe() error {
	if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

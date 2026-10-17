package adminhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/datareplication/internal/usecase"
)

type fakeReporter struct {
	ready bool
	why   string
}

func (f fakeReporter) Status() usecase.CellStatus {
	return usecase.CellStatus{Celula: "1", No: 1, Papel: "lider", Azure: "off", Gate: 7}
}
func (f fakeReporter) Ready() (bool, string) { return f.ready, f.why }

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestAdmin(t *testing.T) {
	h := New(":0", fakeReporter{ready: false, why: "sem líder conhecida"}, nil).srv.Handler
	if rec := get(h, "/livez"); rec.Code != 200 {
		t.Fatalf("livez %d", rec.Code)
	}
	if rec := get(h, "/readyz"); rec.Code != 503 || rec.Body.String() != "sem líder conhecida\n" {
		t.Fatalf("readyz %d %q", rec.Code, rec.Body.String())
	}
	rec := get(h, "/status")
	var st map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st["azure"] != "off" || st["papel"] != "lider" || st["gate"] != float64(7) {
		t.Fatalf("status %s %v", rec.Body.String(), err)
	}
	if rec := get(New(":0", fakeReporter{ready: true}, nil).srv.Handler, "/readyz"); rec.Code != 200 {
		t.Fatalf("readyz pronto %d", rec.Code)
	}
	if rec := get(h, "/metrics"); rec.Code != 404 {
		t.Fatalf("sem handler, /metrics devia ser 404: %d", rec.Code)
	}
	m := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("dr_is_leader 1\n")) })
	if rec := get(New(":0", fakeReporter{}, m).srv.Handler, "/metrics"); rec.Code != 200 || rec.Body.String() != "dr_is_leader 1\n" {
		t.Fatalf("/metrics %d %q", rec.Code, rec.Body.String())
	}
}

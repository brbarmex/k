package etcdraft

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// rebuildPinPrefix marca os pinos de reconstrução: são os únicos que o teto duro solta (RT-15).
const rebuildPinPrefix = "reconstrucao-"

// Retention guarda o que segura o disco do log além do aplicado local (RT-15): os pinos
// (leituras do disco, canal de recuperação, reconstrução) e o piso publicado pela líder.
// Também lista os segmentos .wal do diretório.
type Retention struct {
	dir string

	mu    sync.Mutex
	pins  map[string]pin
	seq   atomic.Uint64 // donos únicos das leituras ("ler-<n>")
	floor atomic.Uint64
}

// pin segura as entradas depois de idx; expires zero não expira.
type pin struct {
	idx     domain.Index
	expires time.Time
}

var (
	_ port.RetentionPins      = (*Retention)(nil)
	_ port.RetentionFloorSink = (*Retention)(nil)
)

func newRetention(dir string) *Retention {
	return &Retention{dir: dir, pins: map[string]pin{}}
}

// Pin registra um pino até release ser chamada.
func (r *Retention) Pin(owner string, idx domain.Index) (release func()) {
	r.mu.Lock()
	r.pins[owner] = pin{idx: idx}
	r.mu.Unlock()
	return func() { r.Unpin(owner) }
}

// PinFor cria ou atualiza um pino que expira depois de ttl (ttl ≤ 0 = sem expiração).
func (r *Retention) PinFor(owner string, idx domain.Index, ttl time.Duration) {
	p := pin{idx: idx}
	if ttl > 0 {
		p.expires = time.Now().Add(ttl)
	}
	r.mu.Lock()
	r.pins[owner] = p
	r.mu.Unlock()
}

func (r *Retention) Unpin(owner string) {
	r.mu.Lock()
	delete(r.pins, owner)
	r.mu.Unlock()
}

// Has diz se o dono ainda tem um pino válido (o teto duro solta os de reconstrução).
func (r *Retention) Has(owner string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.pins[owner]
	return ok && (p.expires.IsZero() || time.Now().Before(p.expires))
}

// readerOwner devolve um dono único para uma leitura do disco.
func (r *Retention) readerOwner() string { return "ler-" + strconv.FormatUint(r.seq.Add(1), 10) }

// MinPin remove os pinos expirados e devolve o menor índice pinado (0 se nenhum).
func (r *Retention) MinPin(now time.Time) domain.Index {
	r.mu.Lock()
	defer r.mu.Unlock()
	var m domain.Index
	found := false
	for owner, p := range r.pins {
		if !p.expires.IsZero() && now.After(p.expires) {
			delete(r.pins, owner)
			continue
		}
		if !found || p.idx < m {
			m, found = p.idx, true
		}
	}
	return m
}

// hasPinAtZero diz se algum pino segura o log desde o começo. Um pino em 0 não aparece no
// MinPin (0 = sem pino na RetentionView), então o Compactor o confere à parte.
func (r *Retention) hasPinAtZero() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pins {
		if p.idx == 0 {
			return true
		}
	}
	return false
}

// Pins conta os pinos ativos.
func (r *Retention) Pins() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pins)
}

// DropRebuildPins solta os pinos de reconstrução (teto duro, RT-15) e diz se havia algum.
func (r *Retention) DropRebuildPins() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := false
	for owner := range r.pins {
		if strings.HasPrefix(owner, rebuildPinPrefix) {
			delete(r.pins, owner)
			dropped = true
		}
	}
	return dropped
}

// hasRebuildPins diz se há pinos de reconstrução.
func (r *Retention) hasRebuildPins() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for owner := range r.pins {
		if strings.HasPrefix(owner, rebuildPinPrefix) {
			return true
		}
	}
	return false
}

// SetFloor guarda o piso de retenção: o calculado pela líder ou o recebido dela.
func (r *Retention) SetFloor(i domain.Index) { r.floor.Store(uint64(i)) }

// Floor é o último piso calculado ou recebido (0 = nada liberado).
func (r *Retention) Floor() domain.Index { return domain.Index(r.floor.Load()) }

// Segments lista os arquivos .wal do diretório, ordenados pela sequência. Ignora os .tmp
// pré-alocados e qualquer arquivo fora do formato %016x-%016x.wal (N-04).
func (r *Retention) Segments() ([]domain.Segment, error) {
	des, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, fmt.Errorf("etcdraft: listar o WAL: %w", err)
	}
	var out []domain.Segment
	for _, de := range des {
		seq, first, ok := parseSegmentName(de.Name())
		if !ok || de.IsDir() {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue // removido entre a listagem e o Stat
		}
		out = append(out, domain.Segment{Seq: seq, First: domain.Index(first), Bytes: info.Size()})
	}
	slices.SortFunc(out, func(a, b domain.Segment) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		}
		return 0
	})
	return out, nil
}

// parseSegmentName lê "%016x-%016x.wal" e confere que o nome é exatamente esse formato.
func parseSegmentName(name string) (seq, first uint64, ok bool) {
	if _, err := fmt.Sscanf(name, "%016x-%016x.wal", &seq, &first); err != nil {
		return 0, 0, false
	}
	return seq, first, segmentName(seq, first) == name
}

func segmentName(seq, first uint64) string { return fmt.Sprintf("%016x-%016x.wal", seq, first) }

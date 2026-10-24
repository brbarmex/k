package usecase

import (
	"sync"
	"time"
)

// rateLog limita um log a uma mensagem por chave a cada `every`, e conta as suprimidas
// para a mensagem seguinte (N-05).
type rateLog struct {
	every time.Duration

	mu         sync.Mutex
	last       map[string]time.Time
	suppressed map[string]int
}

func newRateLog(every time.Duration) *rateLog {
	return &rateLog{every: every, last: map[string]time.Time{}, suppressed: map[string]int{}}
}

// allow diz se a mensagem da chave pode sair agora e quantas foram suprimidas antes dela.
func (r *rateLog) allow(key string, now time.Time) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.last[key]; ok && now.Sub(t) < r.every {
		r.suppressed[key]++
		return false, 0
	}
	n := r.suppressed[key]
	r.last[key], r.suppressed[key] = now, 0
	return true, n
}

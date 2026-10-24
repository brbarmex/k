package domain

import (
	"sync"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestReplClockSampling(t *testing.T) {
	var c ReplicationClock
	c.Reset(t0)
	c.RecordAppend(10, t0)
	c.RecordAppend(11, t0.Add(ms(5)))  // menos de 10 ms: sem amostra
	c.RecordAppend(9, t0.Add(ms(20)))  // índice menor: sem amostra
	c.RecordAppend(10, t0.Add(ms(20))) // índice repetido: sem amostra
	c.RecordAppend(12, t0.Add(ms(20)))
	got := c.Between(0, 100)
	if len(got) != 2 || got[0].Index != 10 || got[1].Index != 12 {
		t.Fatalf("amostras: %+v", got)
	}
}

func TestReplClockCommitAndDelay(t *testing.T) {
	var c ReplicationClock
	c.Reset(t0)
	c.RecordAppend(10, t0)
	c.RecordAppend(20, t0.Add(ms(100)))
	c.RecordAppend(30, t0.Add(ms(200)))
	c.RecordCommit(20, t0.Add(ms(150)))
	if s := c.Between(0, 100); s[0].Committed.IsZero() || s[1].Committed.IsZero() || !s[2].Committed.IsZero() {
		t.Fatalf("commit marcou errado: %+v", s)
	}
	now := t0.Add(ms(1000))
	cases := []struct {
		name            string
		applied, commit Index
		want            time.Duration
		sat             bool
	}{
		{"em dia", 20, 20, 0, false},
		{"à frente do commit", 25, 20, 0, false},
		{"entre amostras usa a seguinte", 12, 20, ms(850), false},
		{"antes da janela", 5, 20, ms(1000), true},
		{"amostra seguinte não commitada", 22, 25, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, sat := c.Delay(tc.applied, tc.commit, now)
			if d != tc.want || sat != tc.sat {
				t.Fatalf("Delay(%d,%d) = %v,%v; quero %v,%v", tc.applied, tc.commit, d, sat, tc.want, tc.sat)
			}
		})
	}
}

// Entradas depois da amostra mais nova (no mesmo intervalo de 10 ms) continuam
// envelhecendo: com a carga parada, uma célula sem elas não aparece em dia.
func TestReplClockDelayAfterNewest(t *testing.T) {
	var c ReplicationClock
	c.Reset(t0)
	c.RecordAppend(100, t0)
	c.RecordAppend(105, t0.Add(ms(3))) // sem amostra
	c.RecordCommit(105, t0.Add(ms(5)))
	d, sat := c.Delay(100, 105, t0.Add(5*time.Second))
	if sat || d != 5*time.Second-ReplClockMinGap {
		t.Fatalf("atraso depois da mais nova: %v %v", d, sat)
	}
}

func TestReplClockEmptyAndReset(t *testing.T) {
	var c ReplicationClock
	c.Reset(t0)
	if d, sat := c.Delay(1, 5, t0.Add(ms(300))); !sat || d != ms(300) {
		t.Fatalf("sem amostras: %v %v", d, sat)
	}
	c.RecordAppend(10, t0)
	c.RecordCommit(10, t0)
	c.Reset(t0.Add(time.Second))
	if len(c.Between(0, 100)) != 0 || c.Horizon(t0.Add(2*time.Second)) != 0 {
		t.Fatal("Reset não zerou")
	}
}

func TestReplClockWrap(t *testing.T) {
	var c ReplicationClock
	c.Reset(t0)
	n := ReplClockCapacity + 10
	for i := 1; i <= n; i++ {
		at := t0.Add(time.Duration(i) * ReplClockMinGap)
		c.RecordAppend(Index(i), at)
		c.RecordCommit(Index(i), at)
	}
	all := c.Between(0, Index(n))
	if len(all) != ReplClockCapacity || all[0].Index != 11 {
		t.Fatalf("anel cheio: %d amostras, primeira %d", len(all), all[0].Index)
	}
	for _, s := range all {
		if s.Committed.IsZero() {
			t.Fatalf("commitPos incoerente: %d sem commit", s.Index)
		}
	}
	now := t0.Add(time.Duration(n+1) * ReplClockMinGap)
	if _, sat := c.Delay(5, Index(n), now); !sat {
		t.Fatal("aplicado antes da janela devia saturar")
	}
	if _, sat := c.Delay(10, Index(n), now); sat {
		t.Fatal("aplicado na borda da janela não satura")
	}
	if h := c.Horizon(now); h != time.Duration(n+1-11)*ReplClockMinGap {
		t.Fatalf("horizonte: %v", h)
	}
}

func TestReplClockConcurrent(t *testing.T) {
	var c ReplicationClock
	c.Reset(t0)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 1; i <= 3_000; i++ {
			at := t0.Add(time.Duration(i) * ReplClockMinGap)
			c.RecordAppend(Index(i), at)
			c.RecordCommit(Index(i-1), at)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 3_000; i++ {
			c.Delay(Index(i/2), Index(i), t0.Add(time.Hour))
			c.Between(Index(i/2), Index(i))
			c.Horizon(t0.Add(time.Hour))
		}
	}()
	wg.Wait()
}

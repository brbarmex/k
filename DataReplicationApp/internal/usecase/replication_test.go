package usecase

import (
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// replConsensus controla papel e commit para o monitor.
type replConsensus struct {
	port.ConsensusEngine
	leader bool
	commit domain.Index
}

func (c *replConsensus) Leader() (port.LeaderInfo, bool) {
	return port.LeaderInfo{NodeID: "1", Local: c.leader}, true
}
func (c *replConsensus) CommitIndex() domain.Index { return c.commit }

// replMetrics guarda as durações das etapas e das recuperações.
type replMetrics struct {
	port.NopMetrics
	mu       sync.Mutex
	stages   map[string][]time.Duration
	recovery map[uint64][]time.Duration
}

func (m *replMetrics) ReplicationStage(etapa string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stages == nil {
		m.stages = map[string][]time.Duration{}
	}
	m.stages[etapa] = append(m.stages[etapa], d)
}

func (m *replMetrics) RecoveryFinished(par uint64, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recovery == nil {
		m.recovery = map[uint64][]time.Duration{}
	}
	m.recovery[par] = append(m.recovery[par], d)
}

type replFixture struct {
	t       *testing.T
	cons    *replConsensus
	clock   *domain.ReplicationClock
	metrics *replMetrics
	local   domain.Index
	mon     *ReplicationMonitor
	now     time.Time
}

func newReplFixture(t *testing.T) *replFixture {
	f := &replFixture{t: t, cons: &replConsensus{leader: true}, clock: &domain.ReplicationClock{},
		metrics: &replMetrics{}, now: time.Unix(1_700_000_000, 0)}
	f.clock.Reset(f.now)
	f.mon = &ReplicationMonitor{Consensus: f.cons, Clock: f.clock, LocalApplied: func() domain.Index { return f.local },
		NodeID: 1, Voters: []uint64{1, 2, 3}, SyncMax: 2 * time.Second, Metrics: f.metrics, Log: zerolog.Nop()}
	return f
}

// write simula uma entrada que entra no log e é commitada 5 ms depois.
func (f *replFixture) write(idx domain.Index) {
	f.clock.RecordAppend(idx, f.now)
	f.clock.RecordCommit(idx, f.now.Add(5*time.Millisecond))
	f.cons.commit = idx
}

func (f *replFixture) advance(d time.Duration) { f.now = f.now.Add(d) }

func (f *replFixture) tick() ReplicationView {
	f.mon.Tick(f.now)
	return f.mon.Snapshot()
}

func (f *replFixture) report(id uint64, applied domain.Index) { f.mon.PeerApplied(id, applied, f.now) }

func TestReplicationAllInSync(t *testing.T) {
	f := newReplFixture(t)
	f.tick() // começa a liderança no commit 0
	for i := domain.Index(1); i <= 5; i++ {
		f.write(i)
		f.advance(20 * time.Millisecond)
	}
	f.local = 5
	f.report(2, 5)
	f.report(3, 5)
	v := f.tick()
	if !v.Lider || len(v.Celulas) != 3 {
		t.Fatalf("view: %+v", v)
	}
	for id, c := range v.Celulas {
		if !c.Sincronizada || c.AtrasoEntradas != 0 || c.Atraso != 0 || c.Recuperando {
			t.Fatalf("célula %d: %+v", id, c)
		}
	}
	for _, e := range []string{"commit", "banco_lider", "banco_quorum", "banco_todas"} {
		if n := len(f.metrics.stages[e]); n != 5 {
			t.Fatalf("etapa %s observada %d vezes, quer 5 (uma por amostra)", e, n)
		}
	}
	f.advance(100 * time.Millisecond)
	f.report(2, 5)
	f.report(3, 5)
	f.tick()
	if n := len(f.metrics.stages["banco_todas"]); n != 5 {
		t.Fatalf("amostra observada duas vezes: %d", n)
	}
}

func TestReplicationPeerOutAndBack(t *testing.T) {
	f := newReplFixture(t)
	f.tick()
	f.write(1)
	f.local = 1
	f.report(2, 1)
	f.report(3, 1)
	f.tick()

	// A célula 3 para de responder; a escrita continua.
	for i := domain.Index(2); i <= 50; i++ {
		f.advance(100 * time.Millisecond)
		f.write(i)
		f.local = i
		f.report(2, i)
		f.tick()
	}
	v := f.tick()
	c3 := v.Celulas[3]
	if c3.Sincronizada || !c3.Recuperando || c3.AtrasoEntradas != 49 || c3.Atraso <= 2*time.Second {
		t.Fatalf("célula 3 devia estar fora: %+v", c3)
	}
	if !v.Celulas[2].Sincronizada {
		t.Fatalf("célula 2 devia estar em dia: %+v", v.Celulas[2])
	}
	if n := len(f.metrics.stages["banco_quorum"]); n < 49 {
		t.Fatalf("banco_quorum devia continuar: %d", n)
	}
	if n := len(f.metrics.stages["banco_todas"]); n != 1 {
		t.Fatalf("banco_todas devia parar com a célula 3 fora: %d", n)
	}

	// A célula 3 volta e alcança.
	f.advance(100 * time.Millisecond)
	start := c3.RecuperandoHa
	f.report(3, 50)
	v = f.tick()
	if !v.Celulas[3].Sincronizada || v.Celulas[3].Recuperando {
		t.Fatalf("célula 3 devia ter voltado: %+v", v.Celulas[3])
	}
	rec := f.metrics.recovery[3]
	if len(rec) != 1 || rec[0] < start {
		t.Fatalf("recuperação: %v (em curso era %v)", rec, start)
	}
	if n := len(f.metrics.stages["banco_todas"]); n != 50 {
		t.Fatalf("banco_todas depois da volta: %d", n)
	}
}

func TestReplicationUnknownPeerGrace(t *testing.T) {
	f := newReplFixture(t)
	f.write(1)
	f.local = 1
	v := f.tick()
	if c := v.Celulas[2]; c.Conhecido || c.Sincronizada || c.Recuperando {
		t.Fatalf("sem notícia no começo: %+v", c)
	}
	f.advance(3 * time.Second)
	v = f.tick()
	if c := v.Celulas[2]; !c.Recuperando || !c.Saturado {
		t.Fatalf("sem notícia depois de SyncMax: %+v", c)
	}
}

func TestReplicationRestartAndStaleReport(t *testing.T) {
	f := newReplFixture(t)
	f.tick()
	for i := domain.Index(1); i <= 10; i++ {
		f.write(i)
		f.advance(20 * time.Millisecond)
	}
	f.local = 10
	f.report(2, 10)
	f.report(3, 10)
	f.tick()

	old := f.now
	f.advance(100 * time.Millisecond)
	f.report(3, 2)                // reinício: o aplicado recua
	f.mon.PeerApplied(3, 10, old) // notícia velha não sobrescreve
	f.advance(3 * time.Second)
	f.report(3, 2)
	f.report(2, 10)
	v := f.tick()
	if c := v.Celulas[3]; c.Aplicado != 2 || !c.Recuperando {
		t.Fatalf("reinício: %+v", c)
	}
	f.advance(100 * time.Millisecond)
	f.report(3, 10)
	f.report(2, 10)
	if v = f.tick(); !v.Celulas[3].Sincronizada || len(f.metrics.recovery[3]) != 1 {
		t.Fatalf("depois do replay: %+v, recuperações %v", v.Celulas[3], f.metrics.recovery[3])
	}
}

func TestReplicationRoleChange(t *testing.T) {
	f := newReplFixture(t)
	f.tick()
	f.write(1)
	f.report(2, 1)
	f.tick()
	f.cons.leader = false
	if v := f.tick(); v.Lider || v.Celulas != nil {
		t.Fatalf("follower: %+v", v)
	}
	if f.mon.reports != nil || f.mon.sessions != nil {
		t.Fatal("estado da liderança não foi limpo")
	}
	f.cons.leader = true
	f.write(2)
	f.clock.Reset(f.now)
	v := f.tick()
	if !v.Lider || v.Celulas[2].Conhecido {
		t.Fatalf("nova liderança começa sem notícias: %+v", v.Celulas[2])
	}
}

package usecase

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// ReplicationMonitor mede, na líder, a replicação até o BANCO de cada célula (D-04):
//
//   - a régua de sincronia (DC-13): uma célula está sincronizada com o atraso de replicação
//     ≤ SyncMax (REPL_SYNC_MAX), medido no relógio da líder (DC-16);
//   - as etapas do tempo de replicação: commit, banco_lider, banco_quorum (2 de 3 bancos)
//     e banco_todas, cada amostra do ReplicationClock observada uma vez por etapa;
//   - as sessões de recuperação (DC-17): da saída da régua até a volta.
//
// O aplicado das followers chega pela resposta do GateUpdate (DC-15, PeerApplied). Nada
// aqui decide gate, commit, piso ou ACK: é só medição. Roda numa goroutine própria, fora
// do laço do Ready.
type ReplicationMonitor struct {
	Consensus    port.ConsensusEngine
	Clock        *domain.ReplicationClock
	LocalApplied func() domain.Index
	NodeID       uint64
	Voters       []uint64
	SyncMax      time.Duration // REPL_SYNC_MAX (DC-13)
	Interval     time.Duration // GATE_PUBLISH_INTERVAL: a cadência das notícias
	Metrics      port.Metrics
	Log          zerolog.Logger

	mu          sync.Mutex
	reports     map[uint64]peerReport
	sessions    map[uint64]time.Time
	cur         stageCursors
	leaderSince time.Time
	wasLeader   bool

	view atomic.Pointer[ReplicationView]
}

var _ port.PeerAppliedSink = (*ReplicationMonitor)(nil)

type peerReport struct {
	applied domain.Index
	at      time.Time
}

// stageCursors guarda, por etapa, o índice da última amostra observada.
type stageCursors struct{ commit, lider, quorum, todas domain.Index }

// ReplicationView é o retrato publicado a cada tick, lido pelo /status e pelo scrape.
// Imutável depois de publicado.
type ReplicationView struct {
	Lider     bool
	SyncMax   time.Duration
	Horizonte time.Duration
	Celulas   map[uint64]CellRepl // por ID do nó, a própria líder incluída
}

// CellRepl é a replicação de uma célula vista pela líder.
type CellRepl struct {
	Aplicado       domain.Index
	Conhecido      bool // a líder tem notícia do aplicado
	IdadeNoticia   time.Duration
	AtrasoEntradas uint64
	Atraso         time.Duration // atraso de replicação (M-11)
	Saturado       bool          // o atraso passou do horizonte das amostras: é um mínimo
	Sincronizada   bool          // dentro de SyncMax (DC-13)
	Recuperando    bool
	RecuperandoHa  time.Duration
}

// PeerApplied guarda o aplicado informado por uma follower (DC-15). Uma notícia mais
// antiga que a guardada é ignorada; um aplicado menor (reinício) é aceito.
func (m *ReplicationMonitor) PeerApplied(id uint64, applied domain.Index, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reports == nil {
		m.reports = map[uint64]peerReport{}
	}
	prev, ok := m.reports[id]
	if ok && !at.After(prev.at) {
		return
	}
	if ok && applied < prev.applied {
		m.Log.Debug().Uint64("par", id).Uint64("antes", uint64(prev.applied)).Uint64("agora", uint64(applied)).
			Msg("aplicado da célula recuou (reinício)")
	}
	m.reports[id] = peerReport{applied: applied, at: at}
}

// Run chama Tick a cada Interval até o contexto acabar.
func (m *ReplicationMonitor) Run(ctx context.Context) error {
	interval := m.Interval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			m.Tick(now)
		}
	}
}

// cellObs é o aplicado de uma célula no instante da notícia.
type cellObs struct {
	id      uint64
	applied domain.Index
	at      time.Time
	known   bool
}

// Tick faz uma medição. Exposto para os testes.
func (m *ReplicationMonitor) Tick(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	metrics := port.MetricsOrNop(m.Metrics)
	if l, ok := m.Consensus.Leader(); !ok || !l.Local {
		if m.wasLeader {
			m.reports, m.sessions, m.cur = nil, nil, stageCursors{}
		}
		m.wasLeader = false
		m.view.Store(&ReplicationView{SyncMax: m.SyncMax})
		return
	}
	commit := m.Consensus.CommitIndex()
	if !m.wasLeader { // nova liderança: mede só o que ela propõe
		m.wasLeader, m.leaderSince = true, now
		m.cur = stageCursors{commit, commit, commit, commit}
		m.sessions = map[uint64]time.Time{}
	}

	obs := make([]cellObs, 0, len(m.Voters))
	for _, id := range m.Voters {
		if id == m.NodeID {
			obs = append(obs, cellObs{id: id, applied: m.LocalApplied(), at: now, known: true})
			continue
		}
		r, ok := m.reports[id]
		obs = append(obs, cellObs{id: id, applied: r.applied, at: r.at, known: ok})
	}

	m.observeStages(now, commit, obs, metrics)

	cells := make(map[uint64]CellRepl, len(obs))
	for _, o := range obs {
		c := CellRepl{Aplicado: o.applied, Conhecido: o.known}
		if o.known {
			c.IdadeNoticia = max(now.Sub(o.at), 0)
			if commit > o.applied {
				c.AtrasoEntradas = uint64(commit - o.applied)
			}
			c.Atraso, c.Saturado = m.Clock.Delay(o.applied, commit, now)
		} else {
			// Sem notícia: o atraso é no mínimo o tempo desde o início da liderança.
			c.Atraso, c.Saturado = max(now.Sub(m.leaderSince), 0), true
		}
		c.Sincronizada = o.known && c.Atraso <= m.SyncMax && c.IdadeNoticia <= m.SyncMax
		// Uma célula sem notícia só sai da régua depois de SyncMax de liderança: as
		// primeiras notícias chegam em um intervalo do GateUpdate.
		out := c.Atraso > m.SyncMax || (o.known && c.IdadeNoticia > m.SyncMax)
		start, inSession := m.sessions[o.id]
		switch {
		case out && !inSession:
			m.sessions[o.id] = now
			start, inSession = now, true
			m.Log.Info().Uint64("par", o.id).Dur("atraso", c.Atraso).Uint64("aplicado", uint64(o.applied)).
				Uint64("commit", uint64(commit)).Bool("conhecido", o.known).Msg("célula fora da régua de sincronia")
		case !out && inSession:
			d := max(now.Sub(start), 0)
			metrics.RecoveryFinished(o.id, d)
			delete(m.sessions, o.id)
			inSession = false
			m.Log.Info().Uint64("par", o.id).Dur("duracao", d).Msg("célula sincronizada de novo")
		}
		if inSession {
			c.Recuperando, c.RecuperandoHa = true, max(now.Sub(start), 0)
		}
		cells[o.id] = c
	}
	m.view.Store(&ReplicationView{Lider: true, SyncMax: m.SyncMax, Celulas: cells})
}

// observeStages avança os cursores das quatro etapas (D-04).
func (m *ReplicationMonitor) observeStages(now time.Time, commit domain.Index, obs []cellObs, metrics port.Metrics) {
	for _, s := range m.Clock.Between(m.cur.commit, commit) {
		if s.Committed.IsZero() {
			break
		}
		metrics.ReplicationStage("commit", max(s.Committed.Sub(s.Appended), 0))
		m.cur.commit = s.Index
	}
	local := min(obs[slices.IndexFunc(obs, func(o cellObs) bool { return o.id == m.NodeID })].applied, commit)
	for _, s := range m.Clock.Between(m.cur.lider, local) {
		if s.Committed.IsZero() {
			break
		}
		metrics.ReplicationStage("banco_lider", max(now.Sub(s.Committed), 0))
		m.cur.lider = s.Index
	}

	var known []cellObs
	for _, o := range obs {
		if o.known {
			known = append(known, o)
		}
	}
	slices.SortFunc(known, func(a, b cellObs) int { // maior aplicado primeiro
		switch {
		case a.applied > b.applied:
			return -1
		case a.applied < b.applied:
			return 1
		}
		return 0
	})
	if q := len(m.Voters)/2 + 1; len(known) >= q {
		m.cur.quorum = m.observeDB("banco_quorum", m.cur.quorum, known[:q], metrics)
	}
	if len(known) == len(m.Voters) {
		m.cur.todas = m.observeDB("banco_todas", m.cur.todas, known, metrics)
	}
}

// observeDB observa as amostras que todas as células do grupo já têm no banco: a duração
// vai da entrada no log até a notícia mais antiga do grupo.
func (m *ReplicationMonitor) observeDB(etapa string, cursor domain.Index, group []cellObs, metrics port.Metrics) domain.Index {
	target, at := group[0].applied, group[0].at
	for _, o := range group[1:] {
		target, at = min(target, o.applied), minTime(at, o.at)
	}
	for _, s := range m.Clock.Between(cursor, target) {
		metrics.ReplicationStage(etapa, max(at.Sub(s.Appended), 0))
		cursor = s.Index
	}
	return cursor
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// Snapshot devolve a última medição, com o horizonte atual das amostras.
func (m *ReplicationMonitor) Snapshot() ReplicationView {
	v := m.view.Load()
	if v == nil {
		return ReplicationView{SyncMax: m.SyncMax}
	}
	out := *v
	if out.Lider && m.Clock != nil {
		out.Horizonte = m.Clock.Horizon(time.Now())
	}
	return out
}

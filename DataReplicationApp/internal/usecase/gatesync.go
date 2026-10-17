package usecase

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// stallTicks é quantos intervalos sem avanço, com commit à frente, contam como gate parado.
const stallTicks = 50

// GateSync liga o gate ao papel da célula e propaga o gate da líder (G-06):
//
//   - a cada intervalo e a cada avanço do gate, informa ao gate o papel (líder ou
//     follower) e o termo atual;
//   - na líder, publica o gate às outras células a cada avanço e, sem avanço, a cada
//     GATE_PUBLISH_INTERVAL;
//   - na follower, OnGateUpdate adota o gate recebido (o commit local continua limitando).
type GateSync struct {
	Consensus port.ConsensusEngine
	Gate      *domain.Gate
	Publisher port.GatePublisher
	Interval  time.Duration // GATE_PUBLISH_INTERVAL (100 ms)
	Log       zerolog.Logger
}

func (s *GateSync) Run(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	var last domain.Index
	idle, stalled := 0, false
	for {
		changed := s.Gate.Changed()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		case <-t.C:
			st := s.Gate.Snapshot()
			if st.Index == last && st.RaftCommit > st.Index {
				idle++
			} else {
				idle, stalled = 0, false
			}
			last = st.Index
			if idle >= stallTicks && !stalled {
				stalled = true
				s.Log.Warn().Uint64("gate", uint64(st.Index)).Uint64("commit", uint64(st.RaftCommit)).
					Uint64("termo", uint64(st.Term)).Msg("gate parado")
			}
		}
		s.Tick()
	}
}

// Tick sincroniza o papel e, na líder, publica o gate. Exposto para os testes.
func (s *GateSync) Tick() {
	l, ok := s.Consensus.Leader()
	leader := ok && l.Local
	s.Gate.SetFollower(!leader)
	s.Gate.SetTerm(s.Consensus.Term())
	if leader {
		s.Publisher.Publish(s.Gate.Index())
	}
}

// OnGateUpdate recebe o gate publicado pela líder.
func (s *GateSync) OnGateUpdate(gate domain.Index) { s.Gate.SetReceived(gate) }

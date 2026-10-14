package usecase

import (
	"testing"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

type fakeRaftStatus struct{ info port.RaftInfo }

func (f fakeRaftStatus) Status() port.RaftInfo { return f.info }

type fakeCatalogReady struct{}

func (fakeCatalogReady) KeySchema(domain.LogicalTable) (domain.KeySchema, bool) {
	return domain.KeySchema{}, false
}
func (fakeCatalogReady) Ready() bool { return true }

func newStatus(info port.RaftInfo) *StatusReporter {
	return &StatusReporter{CellID: "1", NodeID: 1, Raft: fakeRaftStatus{info}, Gate: &domain.Gate{},
		Applier: &Applier{}, Log: &fakeLog{}, Catalog: fakeCatalogReady{}, AzureOff: true}
}

// O-14: o quórum aparece só na líder, com Ok = maioria dos votantes ativa.
func TestStatusQuorumOnlyOnLeader(t *testing.T) {
	cases := []struct {
		name   string
		info   port.RaftInfo
		quorum *QuorumStatus
	}{
		{"líder com todos", port.RaftInfo{NodeID: 1, Lead: 1, QuorumActive: 3, Voters: 3, Running: true}, &QuorumStatus{3, 3, true}},
		{"líder com maioria", port.RaftInfo{NodeID: 1, Lead: 1, QuorumActive: 2, Voters: 3, Running: true}, &QuorumStatus{2, 3, true}},
		{"líder isolada", port.RaftInfo{NodeID: 1, Lead: 1, QuorumActive: 1, Voters: 3, Running: true}, &QuorumStatus{1, 3, false}},
		{"follower", port.RaftInfo{NodeID: 1, Lead: 2, Voters: 3, Running: true}, nil},
		{"sem líder", port.RaftInfo{NodeID: 1, Voters: 3, Running: true}, nil},
	}
	for _, c := range cases {
		got := newStatus(c.info).Status().Quorum
		if (got == nil) != (c.quorum == nil) || (got != nil && *got != *c.quorum) {
			t.Errorf("%s: quórum = %+v, quer %+v", c.name, got, c.quorum)
		}
	}
	if ok, _ := newStatus(port.RaftInfo{NodeID: 1, Lead: 1, Running: true}).Ready(); !ok {
		t.Fatal("célula saudável devia estar pronta")
	}
}

// ADR-058: o /status mostra os workers do applier (pelo menos 1) e os itens em voo.
func TestStatusShowsApplyWorkers(t *testing.T) {
	s := newStatus(port.RaftInfo{NodeID: 1, Lead: 1, Running: true})
	if got := s.Status().Apply; got != (ApplyStatus{Workers: 1, EmVoo: 0}) {
		t.Fatalf("apply = %+v; sem Workers configurado devia ser 1 worker", got)
	}
	s.Applier = &Applier{Workers: 16}
	if got := s.Status().Apply; got.Workers != 16 {
		t.Fatalf("apply = %+v; quer 16 workers", got)
	}
}

// ADR-060: o /status mostra o atraso do apply contra o gate, o teto da admissão, a fila do
// applier, o commit ainda não entregue e a configuração de eleição e banco.
func TestStatusShowsApplyLagAndUndelivered(t *testing.T) {
	cons := newFakeConsensus()
	cons.commitEntries(domain.Entry{Index: 1})
	pump := &CommitPump{Consensus: cons}
	pump.delivered.Store(40)
	s := newStatus(port.RaftInfo{NodeID: 1, Lead: 1, Running: true, Commit: 100})
	s.Gate.SetAzureOff(true)
	s.Gate.SetRaftCommit(90)
	s.Pump, s.ApplyInbox, s.ApplyLagMax, s.ElectionTicks = pump, func() int { return 3 }, 4096, 30
	st := s.Status()
	if st.Apply.Atraso != 90 || st.Apply.AtrasoMax != 4096 || st.Apply.Fila != 3 {
		t.Fatalf("apply = %+v", st.Apply)
	}
	if st.CommitNaoEntregue != 60 || st.ElectionTicks != 30 {
		t.Fatalf("commitNaoEntregue %d, electionTicks %d", st.CommitNaoEntregue, st.ElectionTicks)
	}
}

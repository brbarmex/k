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

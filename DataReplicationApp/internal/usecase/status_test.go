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
		Applier: &Applier{}, Catalog: fakeCatalogReady{}}
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

// O-37, item 6: os blocos raft e wal no /status, com a janela só na líder; o teto duro
// segurado pelo aplicado tira a célula do /readyz.
func TestStatusShowsBoundedLog(t *testing.T) {
	info := port.RaftInfo{NodeID: 1, Lead: 1, Running: true, Snapshot: 900, MemEntries: 110, MemBytes: 4096,
		RetainedBytes: 3 << 20, RetainedFiles: 3, Floor: 850, Pins: 1, BehindTail: true,
		Window: map[uint64]bool{2: true, 3: false}}
	st := newStatus(info).Status()
	if m := st.Raft.Memoria; st.Raft.Snapshot != 900 || !st.Raft.AtrasCauda || m.Entradas != 110 || m.Bytes != 4096 || m.PorPar != nil {
		t.Fatalf("raft = %+v", st.Raft)
	}
	if st.Wal.Retido != (RetidoStatus{Bytes: 3 << 20, Arquivos: 3}) || st.Wal.Piso != 850 || st.Wal.Pinos != 1 ||
		!st.Wal.Janela["2"] || st.Wal.Janela["3"] {
		t.Fatalf("wal = %+v", st.Wal)
	}
	info.HardCeiling = true
	if ok, _ := newStatus(info).Ready(); !ok {
		t.Fatal("teto duro sem o aplicado segurando não tira do /readyz")
	}
	info.HeldByApplied = true
	if ok, why := newStatus(info).Ready(); ok || why != "disco no teto duro: reconstrução necessária" {
		t.Fatalf("Ready = %v %q", ok, why)
	}
}

// O-51: a seção replicacao só aparece na líder; atrasCaudaLider e porPar vêm do RaftInfo.
func TestStatusReplicacao(t *testing.T) {
	f := newReplFixture(t)
	f.tick()
	f.write(5)
	f.local = 5
	f.report(2, 5)
	f.report(3, 1)
	f.mon.Tick(f.now)

	info := port.RaftInfo{NodeID: 1, Lead: 1, Voters: 3, Running: true, Commit: 5, MemMax: 1 << 20, MemFirst: 3,
		Match: map[uint64]domain.Index{1: 5, 2: 5, 3: 1}, MemByPeer: map[uint64]int64{2: 0, 3: 400}}
	st := newStatus(info)
	st.Replication = f.mon
	got := st.Status()
	if got.Raft.Memoria.Teto != 1<<20 || got.Raft.Memoria.Primeiro != 3 || got.Raft.Memoria.PorPar["3"] != 400 {
		t.Fatalf("memória: %+v", got.Raft.Memoria)
	}
	r := got.Replicacao
	if r == nil || len(r.Celulas) != 3 || r.SyncMaxSeg != 2 {
		t.Fatalf("replicacao: %+v", r)
	}
	if c := r.Celulas["3"]; !c.AtrasCaudaLider || c.MemoriaBytes != 400 || c.AtrasoEntradas != 4 || c.Aplicado != 1 {
		t.Fatalf("célula 3: %+v", c)
	}
	if c := r.Celulas["2"]; c.AtrasCaudaLider || !c.Sincronizada {
		t.Fatalf("célula 2: %+v", c)
	}
	if c := r.Celulas["1"]; c.AtrasCaudaLider || !c.Sincronizada {
		t.Fatalf("líder: %+v", c)
	}

	info.Lead = 2 // follower: sem a seção
	st = newStatus(info)
	st.Replication = f.mon
	if got := st.Status(); got.Replicacao != nil {
		t.Fatalf("follower com replicacao: %+v", got.Replicacao)
	}
}

// O-42 (AP-16): o /status mostra o modo, a recuperação e a reconstrução em curso, e o
// /readyz fica falso fora de normal.
func TestStatusCellMode(t *testing.T) {
	s := newStatus(port.RaftInfo{NodeID: 1, Lead: 2, Running: true})
	s.Compaction = CompactacaoStatus{SnapshotEveryEntries: 200, RaftCatchupEntries: 50}
	if st := s.Status(); st.Modo.Estado != "normal" || st.Recuperacao != nil || st.Compactacao.SnapshotEveryEntries != 200 {
		t.Fatalf("sem modo: %+v", st)
	}
	mode := &domain.CellMode{}
	s.Mode = mode
	s.Recovery = &Recovery{Mode: mode}
	s.Rebuild = &Rebuild{Mode: mode}
	if ok, _ := s.Ready(); !ok {
		t.Fatal("normal está pronta")
	}
	mode.EnterRecovering(40)
	mode.SetSource("3")
	st := s.Status()
	if st.Modo != (ModoStatus{Estado: "recuperando", Alvo: 40, Fonte: "3"}) || st.Recuperacao == nil || st.Reconstrucao != nil {
		t.Fatalf("recuperando: %+v %+v", st.Modo, st.Recuperacao)
	}
	if ok, why := s.Ready(); ok || why != "célula em recuperando" {
		t.Fatalf("readyz recuperando: %v %q", ok, why)
	}
	mode.EnterRebuilding()
	st = s.Status()
	if st.Modo.Estado != "reconstruindo" || st.Reconstrucao == nil || st.Recuperacao != nil {
		t.Fatalf("reconstruindo: %+v", st)
	}
	if ok, why := s.Ready(); ok || why != "célula em reconstruindo" {
		t.Fatalf("readyz reconstruindo: %v %q", ok, why)
	}
}

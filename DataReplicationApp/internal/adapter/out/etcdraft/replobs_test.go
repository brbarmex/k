package etcdraft

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
)

// O-49: bytesAfter soma o que a RAM segura por uma réplica com o Match dado (DC-14).
func TestRAMTrackerBytesAfter(t *testing.T) {
	var r ramTracker
	r.recordSnapshot(1)
	var ents []*raftpb.Entry
	for i := uint64(2); i <= 11; i++ {
		ents = append(ents, ent(i, 1, 100))
	}
	r.recordAppend(ents)
	sz := int64(proto.Size(ent(2, 1, 100)))
	for _, tc := range []struct {
		idx  domain.Index
		want int64
	}{
		{0, 10 * sz}, // antes do primeiro guardado: tudo
		{1, 10 * sz}, // Match no snapshot: tudo
		{5, 6 * sz},  // depois de 5: 6..11
		{11, 0},      // em dia
		{50, 0},      // à frente
	} {
		if got := r.bytesAfter(tc.idx); got != tc.want {
			t.Errorf("bytesAfter(%d) = %d, quer %d", tc.idx, got, tc.want)
		}
	}
}

// newClockedCluster sobe 3 nós com a compactação e um ReplicationClock por nó.
func newClockedCluster(t *testing.T, ctx context.Context, ramMax int64) (*cluster, map[uint64]*domain.ReplicationClock) {
	base := t.TempDir()
	c := &cluster{
		t: t, net: &memNet{nodes: map[uint64]*Node{}, down: map[uint64]bool{}, ctx: ctx},
		dirs: map[uint64]string{}, cancel: map[uint64]context.CancelFunc{}, done: map[uint64]chan error{},
		got: map[uint64]*collector{}, m: map[uint64]*recMetrics{}, tune: map[uint64]func(*Config){},
		paused: map[uint64]bool{}, cdone: map[uint64]chan struct{}{},
	}
	clocks := map[uint64]*domain.ReplicationClock{}
	for id := uint64(1); id <= 3; id++ {
		c.dirs[id] = filepath.Join(base, "n"+strconv.FormatUint(id, 10), "wal")
		compacting(c, id, 100, 10, 1<<30, ramMax)
		clk := &domain.ReplicationClock{}
		clocks[id] = clk
		inner := c.tune[id]
		c.tune[id] = func(cfg *Config) { inner(cfg); cfg.ReplClock = clk }
		c.start(ctx, id, []uint64{1, 2, 3})
	}
	return c, clocks
}

// O-49: só a líder grava amostras; na troca de líder, a nova começa vazia (DC-16).
func TestReplClockOnlyOnLeader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, clocks := newClockedCluster(t, ctx, 1<<30)
	defer stopAll(c)
	l := c.leader(5 * time.Second)
	proposeN(t, ctx, l, 300)
	commit := l.CommitIndex()
	waitFor(t, 2*time.Second, func() bool {
		s := clocks[l.cfg.ID].Between(0, commit)
		return len(s) > 0 && !s[len(s)-1].Committed.IsZero()
	}, "a líder não tem amostras commitadas")
	for id, clk := range clocks {
		if id != l.cfg.ID && len(clk.Between(0, 1<<62)) != 0 {
			t.Fatalf("follower %d gravou amostras", id)
		}
	}

	old := l.cfg.ID
	c.stop(old)
	nl := c.leader(10 * time.Second)
	if s := clocks[nl.cfg.ID].Between(0, commit); len(s) != 0 {
		t.Fatalf("nova líder com %d amostras da liderança anterior", len(s))
	}
	proposeN(t, ctx, nl, 50)
	waitFor(t, 2*time.Second, func() bool { return len(clocks[nl.cfg.ID].Between(commit, 1<<62)) > 0 },
		"a nova líder não gravou amostras")
}

// O-49: MemByPeer mostra o que a líder segura pela follower parada; acima do teto da
// RAM, o Compactor registra RAMCeilingHit (DC-14, SG-23).
func TestMemByPeerAndCeiling(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ramMax  int64
		ceiling bool
	}{
		{"dentro do teto", 1 << 30, false},
		{"teto atingido", 16 << 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := newClockedCluster(t, ctx, tc.ramMax)
			defer stopAll(c)
			l := c.leader(5 * time.Second)
			var down, up uint64
			for id := range c.dirs {
				if id == l.cfg.ID {
					continue
				}
				if down == 0 {
					down = id
				} else {
					up = id
				}
			}
			c.stop(down)
			proposeN(t, ctx, l, 600)
			time.Sleep(200 * time.Millisecond) // alguns ciclos do Compactor
			st := l.Status()
			if st.MemMax != tc.ramMax || st.MemFirst == 0 {
				t.Fatalf("MemMax %d MemFirst %d", st.MemMax, st.MemFirst)
			}
			if tc.ceiling {
				if c.m[l.cfg.ID].n("ram_ceiling") == 0 {
					t.Fatal("RAMCeilingHit não registrado acima do teto")
				}
				if st.Match[down]+1 >= st.MemFirst {
					t.Fatalf("follower parada devia estar atrás da RAM: match %d, primeiro %d", st.Match[down], st.MemFirst)
				}
				return
			}
			if c.m[l.cfg.ID].n("ram_ceiling") != 0 {
				t.Fatal("RAMCeilingHit registrado dentro do teto")
			}
			if st.MemByPeer[down] <= 0 || st.MemByPeer[down] <= st.MemByPeer[up] {
				t.Fatalf("MemByPeer = %v: a parada (%d) devia segurar mais que a em dia (%d)", st.MemByPeer, down, up)
			}
			for id := range c.dirs {
				if id != l.cfg.ID && id != down {
					if fs := c.net.nodes[id].Status(); fs.MemByPeer != nil {
						t.Fatalf("follower %d com MemByPeer", id)
					}
				}
			}
		})
	}
}

// DC-19: uma mensagem do Raft para outro nó (a conexão de um par caiu noutra célula) é
// descartada no Step: o heartbeat com o commit de outro par não derruba a célula.
func TestStepDropsMessagesForOtherNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCluster(t, ctx)
	defer func() {
		for id := range c.dirs {
			c.stop(id)
		}
	}()
	l := c.leader(5 * time.Second)
	var f *Node
	for id, n := range c.net.nodes {
		if id != l.cfg.ID {
			f = n
			break
		}
	}
	other := uint64(6) - l.cfg.ID - f.cfg.ID
	hb := &raftpb.Message{Type: raftpb.MsgHeartbeat.Enum(), To: proto.Uint64(other), From: proto.Uint64(l.cfg.ID),
		Term: proto.Uint64(uint64(l.Term())), Commit: proto.Uint64(uint64(f.CommitIndex()) + 1000)}
	data, err := proto.Marshal(hb)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Step(ctx, [][]byte{data}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // o Raft entraria em pânico aqui (tocommit fora do log)
	if f.CommitIndex() >= 1000 {
		t.Fatal("a mensagem para outro nó chegou ao Raft")
	}
}

package domain

import "testing"

// DC-12: a decisão da partida com o volume do Raft vazio.
func TestDecideBoot(t *testing.T) {
	empty := func(tok string) BootPeer {
		return BootPeer{Responded: true, ClusterID: "poc", EmptyVolume: true, BootToken: tok}
	}
	running := func(gen map[uint64]string) BootPeer {
		return BootPeer{Responded: true, ClusterID: "poc", Genesis: gen}
	}
	cases := []struct {
		name   string
		local  bool
		peers  map[uint64]BootPeer
		want   BootKind
		reason string
	}{
		{"todos vazios", false, map[uint64]BootPeer{2: empty("t2"), 3: empty("t3")}, BootCreate, ""},
		{"um sem resposta", false, map[uint64]BootPeer{2: empty("t2"), 3: {}}, BootWait, ""},
		{"rodando com o token certo", false, map[uint64]BootPeer{2: running(map[uint64]string{1: "t1", 2: "t2", 3: "t3"}),
			3: running(map[uint64]string{1: "t1", 2: "t2", 3: "t3"})}, BootCreate, ""},
		{"rodando com outro token", false, map[uint64]BootPeer{2: running(map[uint64]string{1: "velho"}), 3: empty("t3")},
			BootRefuse, "o grupo já existe e não nasceu com esta célula"},
		{"rodando sem Genesis[self]", false, map[uint64]BootPeer{2: running(nil), 3: {}}, BootRefuse,
			"o grupo já existe e não nasceu com esta célula"},
		{"outro grupo", false, map[uint64]BootPeer{2: {Responded: true, ClusterID: "outro", EmptyVolume: true}, 3: empty("t3")},
			BootRefuse, "par de outro grupo"},
		{"tabela local com itens", true, map[uint64]BootPeer{2: empty("t2"), 3: empty("t3")}, BootRefuse,
			"volume do Raft vazio com a tabela local já com itens"},
	}
	for _, c := range cases {
		d := DecideBoot(1, "poc", "t1", c.local, c.peers)
		if d.Kind != c.want || d.Reason != c.reason {
			t.Fatalf("%s: %+v", c.name, d)
		}
	}
	d := DecideBoot(1, "poc", "t1", false, map[uint64]BootPeer{2: empty("t2"), 3: running(map[uint64]string{1: "t1", 2: "t2", 3: "t3"})})
	if d.Kind != BootCreate || d.Genesis[1] != "t1" || d.Genesis[2] != "t2" || d.Genesis[3] != "t3" {
		t.Fatalf("Genesis: %+v", d)
	}
	if d := DecideBoot(1, "poc", "t1", false, map[uint64]BootPeer{2: {}, 3: {}}); d.Kind != BootWait || len(d.Waiting) != 2 {
		t.Fatalf("esperando: %+v", d)
	}
}

// DC-12 (O-42, item 7): três células vazias partindo com atrasos diferentes sobem todas;
// uma célula que reinicia antes de criar o WAL reusa o token e sobe; uma célula com o
// volume apagado depois de o grupo existir é recusada.
func TestBootThreeCellsSimulation(t *testing.T) {
	type cell struct {
		token   string
		up      bool              // já respondeu (pré-partida ou rodando)
		genesis map[uint64]string // nil = ainda sem WAL
	}
	cells := map[uint64]*cell{1: {token: "t1", up: true}, 2: {token: "t2", up: true}, 3: {token: "t3"}}
	view := func(self uint64) map[uint64]BootPeer {
		out := map[uint64]BootPeer{}
		for id, c := range cells {
			if id == self {
				continue
			}
			switch {
			case !c.up:
				out[id] = BootPeer{}
			case c.genesis == nil:
				out[id] = BootPeer{Responded: true, ClusterID: "poc", EmptyVolume: true, BootToken: c.token}
			default:
				out[id] = BootPeer{Responded: true, ClusterID: "poc", Genesis: c.genesis}
			}
		}
		return out
	}
	decide := func(id uint64) BootDecision {
		return DecideBoot(id, "poc", cells[id].token, false, view(id))
	}
	if d := decide(1); d.Kind != BootWait { // a 3 ainda não subiu
		t.Fatalf("célula 1 sem a 3: %+v", d)
	}
	cells[3].up = true // a 3 sobe 5 s depois
	d1 := decide(1)
	if d1.Kind != BootCreate {
		t.Fatalf("célula 1: %+v", d1)
	}
	cells[1].genesis = d1.Genesis
	// A 2 reinicia antes de criar o WAL: o token ficou no volume e é o mesmo.
	d2 := decide(2)
	if d2.Kind != BootCreate {
		t.Fatalf("célula 2 depois do reinício: %+v", d2)
	}
	cells[2].genesis = d2.Genesis
	d3 := decide(3) // a lenta encontra as outras já rodando, com o token dela no Genesis
	if d3.Kind != BootCreate {
		t.Fatalf("célula 3 atrasada: %+v", d3)
	}
	cells[3].genesis = d3.Genesis
	for id, c := range cells {
		for other, tok := range map[uint64]string{1: "t1", 2: "t2", 3: "t3"} {
			if c.genesis[other] != tok {
				t.Fatalf("célula %d: Genesis %v", id, c.genesis)
			}
		}
	}
	// A 2 perde o volume: volta com outro token e sem WAL.
	cells[2] = &cell{token: "novo", up: true}
	if d := decide(2); d.Kind != BootRefuse || d.Reason != "o grupo já existe e não nasceu com esta célula" {
		t.Fatalf("volume perdido: %+v", d)
	}
}

package usecase

import (
	"strconv"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// CellStatus é o retrato da célula para o /status (S.7).
type CellStatus struct {
	Celula   string       `json:"celula"`
	No       uint64       `json:"no"`
	Papel    string       `json:"papel"`
	Lider    uint64       `json:"lider"`
	Termo    domain.Term  `json:"termo"`
	Commit   domain.Index `json:"commit"`
	Gate     domain.Index `json:"gate"`
	Aplicado domain.Index `json:"aplicado"`
	Apply    ApplyStatus  `json:"apply"`
	// CommitNaoEntregue: entradas commitadas que o CommitPump ainda não entregou ao
	// applier (ADR-060). Ficam na memória do Raft; o laço do Raft não espera por elas.
	CommitNaoEntregue uint64            `json:"commitNaoEntregue"`
	ElectionTicks     int               `json:"electionTicks"`
	ErroApply         string            `json:"erroApply,omitempty"`
	Match             map[string]uint64 `json:"match,omitempty"`
	Quorum            *QuorumStatus     `json:"quorum,omitempty"` // só na líder

	// Log do Raft limitado (O-37, item 6; S.7 da trecho).
	Raft RaftLogStatus `json:"raft"`
	Wal  WalStatus     `json:"wal"`

	// Replicacao é a replicação até o banco de cada célula, vista pela líder (D-04,
	// DC-13). Só na líder.
	Replicacao *ReplicacaoStatus `json:"replicacao,omitempty"`

	// Estado da célula diante do log (AP-16, S.7 da trecho). A chave é "modo" porque
	// "celula" já é o ID da célula.
	Modo         ModoStatus          `json:"modo"`
	Recuperacao  *RecuperacaoStatus  `json:"recuperacao,omitempty"`
	Reconstrucao *ReconstrucaoStatus `json:"reconstrucao,omitempty"`
	Compactacao  CompactacaoStatus   `json:"compactacao"`
}

// ModoStatus é o estado da célula: normal, recuperando ou reconstruindo.
type ModoStatus struct {
	Estado string       `json:"estado"`
	Alvo   domain.Index `json:"alvo,omitempty"`
	Fonte  string       `json:"fonte,omitempty"`
}

// RecuperacaoStatus é a recuperação em curso pelo CatchUp (AP-17).
type RecuperacaoStatus struct {
	Fonte     uint64       `json:"fonte"`
	De        domain.Index `json:"de"`
	Ate       domain.Index `json:"ate"`
	Alcancado domain.Index `json:"alcancado"`
}

// ReconstrucaoStatus é a reconstrução em curso pelo CopyTable (AP-18). Recusada traz o
// motivo da recusa (DC-11): a célula fica fora até um novo POST /admin/reconstruir.
type ReconstrucaoStatus struct {
	Fonte         uint64              `json:"fonte,omitempty"`
	Tabela        domain.LogicalTable `json:"tabela,omitempty"`
	ItensCopiados uint64              `json:"itensCopiados"`
	F             domain.Index        `json:"f"`
	Recusada      string              `json:"recusada,omitempty"`
}

// CompactacaoStatus é a configuração da compactação do log, que as tasks de teste conferem.
type CompactacaoStatus struct {
	SnapshotEveryEntries uint64 `json:"snapshotEveryEntries"`
	RaftCatchupEntries   uint64 `json:"raftCatchupEntries"`
	RaftRAMMax           int64  `json:"raftRamMax"`
	RetainMaxBytes       int64  `json:"retainMaxBytes"`
	WalSegmentBytes      int64  `json:"walSegmentBytes"`
	RetainDiskHardBytes  int64  `json:"retainDiskHardBytes"`
}

// ReplicacaoStatus é a régua de sincronia e cada célula (a própria líder incluída).
type ReplicacaoStatus struct {
	SyncMaxSeg   float64                     `json:"syncMaxSeg"`
	HorizonteSeg float64                     `json:"horizonteSeg"`
	Celulas      map[string]ReplicacaoCelula `json:"celulas"`
}

// ReplicacaoCelula é uma célula vista pela líder. Aplicado é a versão _dr_v garantida no
// banco dela; AtrasCaudaLider = a líder não tem mais na RAM o que ela precisa (volta pelo
// CatchUp ou pela reconstrução); MemoriaBytes = o que a RAM da líder segura por ela (DC-14).
type ReplicacaoCelula struct {
	Aplicado         uint64  `json:"aplicado"`
	Conhecido        bool    `json:"conhecido"`
	IdadeNoticiaSeg  float64 `json:"idadeNoticiaSeg"`
	AtrasoEntradas   uint64  `json:"atrasoEntradas"`
	AtrasoSeg        float64 `json:"atrasoSeg"`
	Saturado         bool    `json:"saturado"`
	Sincronizada     bool    `json:"sincronizada"`
	Recuperando      bool    `json:"recuperando"`
	RecuperandoHaSeg float64 `json:"recuperandoHaSeg"`
	AtrasCaudaLider  bool    `json:"atrasCaudaLider"`
	MemoriaBytes     int64   `json:"memoriaBytes"`
}

// RaftLogStatus é o log do Raft na RAM: o último snapshot-marcador e o que sobrou no
// MemoryStorage (RT-11, RT-14).
type RaftLogStatus struct {
	Snapshot domain.Index  `json:"snapshot"`
	Memoria  MemoriaStatus `json:"memoria"`
	// AtrasCauda: a célula recebeu um marcador à frente do seu aplicado e está em
	// recuperação (AP-17).
	AtrasCauda bool `json:"atrasCauda"`
}

type MemoriaStatus struct {
	Entradas uint64 `json:"entradas"`
	Bytes    int64  `json:"bytes"`
	// Teto é o RAFT_RAM_MAX; Primeiro, o primeiro índice na RAM; PorPar, os bytes desde o
	// Match de cada par (só na líder, DC-14).
	Teto     int64            `json:"teto"`
	Primeiro domain.Index     `json:"primeiro"`
	PorPar   map[string]int64 `json:"porPar,omitempty"`
}

// WalStatus é o log do Raft no disco (RT-15).
type WalStatus struct {
	Retido   RetidoStatus `json:"retido"`
	Piso     domain.Index `json:"piso"`
	Pinos    int          `json:"pinos"`
	TetoDuro bool         `json:"tetoDuro"`
	// SeguradoPeloAplicado: no teto duro, quem segura o disco é o aplicado local.
	SeguradoPeloAplicado bool            `json:"seguradoPeloAplicado,omitempty"`
	Janela               map[string]bool `json:"janela,omitempty"` // só na líder
}

type RetidoStatus struct {
	Bytes    int64 `json:"bytes"`
	Arquivos int   `json:"arquivos"`
}

// QuorumStatus é o quórum visto pela líder: votantes ativos recentemente (contando ela).
type QuorumStatus struct {
	Ativos   int  `json:"ativos"`
	Votantes int  `json:"votantes"`
	Ok       bool `json:"ok"`
}

// ApplyStatus é o applier visto de fora (ADR-058): quantos workers gravam em paralelo e
// quantos itens estão a caminho do banco. "aplicado" é a marca d'água.
type ApplyStatus struct {
	Workers int `json:"workers"`
	EmVoo   int `json:"emVoo"`
	// Atraso = gate - aplicado; acima de AtrasoMax (APPLY_LAG_MAX) a líder recusa escritas
	// novas com ThrottlingException (ADR-060).
	Atraso    uint64 `json:"atraso"`
	AtrasoMax uint64 `json:"atrasoMax"`
	// Fila: lotes entregues pelo CommitPump e ainda não despachados pelo applier.
	Fila int `json:"fila"`
}

// StatusReporter agrega Raft, gate, applier e catálogo.
type StatusReporter struct {
	CellID  string
	NodeID  uint64
	Raft    port.RaftStatus
	Gate    *domain.Gate
	Applier *Applier
	Catalog port.Catalog

	// Opcionais (ADR-060): o CommitPump (commit não entregue), o tamanho da fila do
	// applier e a configuração mostrada no /status.
	Pump          *CommitPump
	ApplyInbox    func() int
	ApplyLagMax   uint64
	ElectionTicks int

	// Replication é o monitor da replicação até o banco (D-04); nulo = sem a seção.
	Replication *ReplicationMonitor

	// Estado da célula (AP-16): o modo, a recuperação e a reconstrução em curso; nulos =
	// sempre normal. Compaction é a configuração mostrada no /status.
	Mode       *domain.CellMode
	Recovery   *Recovery
	Rebuild    *Rebuild
	Compaction CompactacaoStatus
}

func (r *StatusReporter) Status() CellStatus {
	ri := r.Raft.Status()
	g := r.Gate.Snapshot()
	st := CellStatus{
		Celula: r.CellID, No: r.NodeID, Papel: "follower", Lider: ri.Lead, Termo: ri.Term, Commit: ri.Commit,
		Gate: g.Index, Aplicado: r.Applier.Applied(),
		Apply: ApplyStatus{Workers: r.Applier.WorkerCount(), EmVoo: r.Applier.InFlight()},
	}
	st.Apply.AtrasoMax = r.ApplyLagMax
	if st.Gate > st.Aplicado {
		st.Apply.Atraso = uint64(st.Gate - st.Aplicado)
	}
	if r.ApplyInbox != nil {
		st.Apply.Fila = r.ApplyInbox()
	}
	if r.Pump != nil {
		if d := r.Pump.Delivered(); st.Commit > d {
			st.CommitNaoEntregue = uint64(st.Commit - d)
		}
	}
	st.ElectionTicks = r.ElectionTicks
	st.Raft = RaftLogStatus{Snapshot: ri.Snapshot, Memoria: MemoriaStatus{Entradas: ri.MemEntries, Bytes: ri.MemBytes,
		Teto: ri.MemMax, Primeiro: ri.MemFirst}, AtrasCauda: ri.BehindTail}
	if len(ri.MemByPeer) > 0 {
		st.Raft.Memoria.PorPar = make(map[string]int64, len(ri.MemByPeer))
		for id, b := range ri.MemByPeer {
			st.Raft.Memoria.PorPar[strconv.FormatUint(id, 10)] = b
		}
	}
	st.Wal = WalStatus{Retido: RetidoStatus{Bytes: ri.RetainedBytes, Arquivos: ri.RetainedFiles}, Piso: ri.Floor,
		Pinos: ri.Pins, TetoDuro: ri.HardCeiling, SeguradoPeloAplicado: ri.HeldByApplied}
	if len(ri.Window) > 0 {
		st.Wal.Janela = make(map[string]bool, len(ri.Window))
		for id, in := range ri.Window {
			st.Wal.Janela[strconv.FormatUint(id, 10)] = in
		}
	}
	if ri.Lead != 0 && ri.Lead == r.NodeID {
		st.Papel = "lider"
		st.Match = make(map[string]uint64, len(ri.Match))
		for id, m := range ri.Match {
			st.Match[strconv.FormatUint(id, 10)] = uint64(m)
		}
		st.Quorum = &QuorumStatus{Ativos: ri.QuorumActive, Votantes: ri.Voters, Ok: ri.QuorumActive >= ri.Voters/2+1}
		st.Replicacao = r.replicacao(ri)
	}
	st.Compactacao = r.Compaction
	st.Modo.Estado = domain.CellNormal.String()
	if r.Mode != nil {
		state := r.Mode.State()
		st.Modo = ModoStatus{Estado: state.String(), Alvo: r.Mode.Target(), Fonte: r.Mode.Source()}
		if state == domain.CellRecovering && r.Recovery != nil {
			p := r.Recovery.Progress()
			st.Recuperacao = &RecuperacaoStatus{Fonte: p.Fonte, De: p.De, Ate: p.Ate, Alcancado: p.Alcancado}
		}
		if state == domain.CellRebuilding && r.Rebuild != nil {
			b := r.Rebuild.Status()
			st.Reconstrucao = &ReconstrucaoStatus{Fonte: b.Fonte, Tabela: b.Tabela, ItensCopiados: b.ItensCopiados, F: b.F,
				Recusada: b.Recusada}
		}
	}
	if h := r.Applier.Health(); h.Stopped {
		st.ErroApply = "parado em (" + strconv.FormatUint(uint64(h.Index), 10) + ", " +
			strconv.FormatUint(uint64(h.Ord), 10) + "): " + h.Err
	}
	return st
}

// Ready diz se a célula está pronta (S.7): tabela conferida, Raft rodando, líder
// conhecida e applier sem erro. Devolve o motivo quando não está.
func (r *StatusReporter) Ready() (bool, string) {
	ri := r.Raft.Status()
	switch {
	case !r.Catalog.Ready():
		return false, "tabelas ainda não conferidas"
	case !ri.Running:
		return false, "raft parado"
	case ri.Lead == 0:
		return false, "sem líder conhecida"
	case r.Applier.Health().Stopped:
		return false, "applier parado"
	case r.Mode != nil && r.Mode.State() != domain.CellNormal:
		return false, "célula em " + r.Mode.State().String()
	case ri.HardCeiling && ri.HeldByApplied:
		return false, "disco no teto duro: reconstrução necessária"
	}
	return true, ""
}

// replicacao monta a seção de replicação do /status a partir da última medição do monitor.
func (r *StatusReporter) replicacao(ri port.RaftInfo) *ReplicacaoStatus {
	if r.Replication == nil {
		return nil
	}
	v := r.Replication.Snapshot()
	if !v.Lider {
		return nil
	}
	out := &ReplicacaoStatus{SyncMaxSeg: v.SyncMax.Seconds(), HorizonteSeg: v.Horizonte.Seconds(),
		Celulas: make(map[string]ReplicacaoCelula, len(v.Celulas))}
	for id, c := range v.Celulas {
		rc := ReplicacaoCelula{Aplicado: uint64(c.Aplicado), Conhecido: c.Conhecido,
			IdadeNoticiaSeg: c.IdadeNoticia.Seconds(), AtrasoEntradas: c.AtrasoEntradas, AtrasoSeg: c.Atraso.Seconds(),
			Saturado: c.Saturado, Sincronizada: c.Sincronizada, Recuperando: c.Recuperando,
			RecuperandoHaSeg: c.RecuperandoHa.Seconds(), MemoriaBytes: ri.MemByPeer[id]}
		if m, ok := ri.Match[id]; ok && id != r.NodeID {
			rc.AtrasCaudaLider = m+1 < ri.MemFirst
		}
		out.Celulas[strconv.FormatUint(id, 10)] = rc
	}
	return out
}

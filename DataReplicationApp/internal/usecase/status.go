package usecase

import (
	"strconv"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// CellStatus é o retrato da célula para o /status (S.7).
type CellStatus struct {
	Celula     string            `json:"celula"`
	No         uint64            `json:"no"`
	Papel      string            `json:"papel"`
	Lider      uint64            `json:"lider"`
	Termo      domain.Term       `json:"termo"`
	Commit     domain.Index      `json:"commit"`
	Gate       domain.Index      `json:"gate"`
	GatePronto bool              `json:"gatePronto"`
	Aplicado   domain.Index      `json:"aplicado"`
	Azure      string            `json:"azure"`
	Replog     ReplogStatus      `json:"replog"`
	ErroApply  string            `json:"erroApply,omitempty"`
	Match      map[string]uint64 `json:"match,omitempty"`
}

// ReplogStatus é o estado do log de replicação.
type ReplogStatus struct {
	Ultimo  domain.Index `json:"ultimo"`
	Duravel domain.Index `json:"duravel"`
}

// StatusReporter agrega Raft, gate, applier, log de replicação e catálogo.
type StatusReporter struct {
	CellID   string
	NodeID   uint64
	Raft     port.RaftStatus
	Gate     *domain.Gate
	Applier  *Applier
	Log      port.ReplicationLog
	Catalog  port.Catalog
	AzureOff bool
}

func (r *StatusReporter) Status() CellStatus {
	ri := r.Raft.Status()
	g := r.Gate.Snapshot()
	st := CellStatus{
		Celula: r.CellID, No: r.NodeID, Papel: "follower", Lider: ri.Lead, Termo: ri.Term, Commit: ri.Commit,
		Gate: g.Index, GatePronto: g.Ready, Aplicado: r.Applier.Applied(), Azure: "on",
		Replog: ReplogStatus{Ultimo: r.Log.LastIndex(), Duravel: r.Log.DurableIndex()},
	}
	if r.AzureOff {
		st.Azure = "off"
	}
	if ri.Lead != 0 && ri.Lead == r.NodeID {
		st.Papel = "lider"
		st.Match = make(map[string]uint64, len(ri.Match))
		for id, m := range ri.Match {
			st.Match[strconv.FormatUint(id, 10)] = uint64(m)
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
	}
	return true, ""
}

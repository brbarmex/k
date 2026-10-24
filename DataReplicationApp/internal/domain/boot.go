package domain

import (
	"slices"
)

// BootKind é a decisão da partida de uma célula com o volume do Raft vazio (DC-12).
type BootKind uint8

const (
	// BootWait: falta a resposta de algum par; na dúvida, espera (não cria).
	BootWait BootKind = iota
	// BootCreate: o grupo está nascendo (ou já nasceu com esta célula): cria o WAL com o
	// Genesis.
	BootCreate
	// BootRefuse: o grupo já existe sem esta célula; ela não sobe (ErrLostRaftVolume).
	BootRefuse
)

// BootPeer é a resposta de um par ao Retained na pré-partida. Responded false = sem resposta.
type BootPeer struct {
	Responded   bool
	ClusterID   string
	EmptyVolume bool
	BootToken   string
	Genesis     map[uint64]string
}

// BootDecision é o resultado de DecideBoot.
type BootDecision struct {
	Kind    BootKind
	Reason  string            // em BootRefuse
	Genesis map[uint64]string // em BootCreate
	Waiting []uint64          // em BootWait: os pares sem resposta, em ordem
}

// DecideBoot decide a partida de uma célula com o volume do Raft vazio (DC-12), na ordem:
//
//  1. a tabela local já tem itens → recusa;
//  2. um par de outro grupo → recusa;
//  3. um par rodando cujo Genesis não tem o token desta célula → recusa (o grupo já existe
//     e não nasceu com ela: ela perdeu o volume);
//  4. um par sem resposta → espera;
//  5. senão, cria, com o Genesis = o token de cada par vazio, o Genesis de cada par
//     rodando e o próprio token.
//
// O token prova que os pares viram esta célula vazia antes de ela ter qualquer estado:
// fica no volume e sobrevive a uma queda no nascimento; um volume perdido perde o token.
func DecideBoot(self uint64, clusterID, bootToken string, localHasItems bool, peers map[uint64]BootPeer) BootDecision {
	if localHasItems {
		return BootDecision{Kind: BootRefuse, Reason: "volume do Raft vazio com a tabela local já com itens"}
	}
	ids := make([]uint64, 0, len(peers))
	for id := range peers {
		if id != self {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		if p := peers[id]; p.Responded && p.ClusterID != clusterID {
			return BootDecision{Kind: BootRefuse, Reason: "par de outro grupo"}
		}
	}
	for _, id := range ids {
		if p := peers[id]; p.Responded && !p.EmptyVolume && p.Genesis[self] != bootToken {
			return BootDecision{Kind: BootRefuse, Reason: "o grupo já existe e não nasceu com esta célula"}
		}
	}
	var waiting []uint64
	for _, id := range ids {
		if !peers[id].Responded {
			waiting = append(waiting, id)
		}
	}
	if len(waiting) > 0 {
		return BootDecision{Kind: BootWait, Waiting: waiting}
	}
	genesis := map[uint64]string{self: bootToken}
	for _, id := range ids {
		p := peers[id]
		if p.EmptyVolume {
			genesis[id] = p.BootToken
			continue
		}
		for k, v := range p.Genesis {
			if _, ok := genesis[k]; !ok {
				genesis[k] = v
			}
		}
	}
	return BootDecision{Kind: BootCreate, Genesis: genesis}
}

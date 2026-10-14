package domain

// Regra do que pode sair antes do fsync do lote (ADR-056, 5.5.2). Função pura para o
// adaptador etcdraft, os testes e o modelo TLA+ usarem a mesma regra.
//
// Invariante: nenhum ACK, voto ou resposta de AppendEntries sai antes do fsync que cobre
// a entrada. No modo síncrono com F3, a única exceção é o MsgApp da líder: o par só
// responde depois do próprio fsync e a líder só se conta no quórum depois do dela.

// MsgKind é o tipo de mensagem do Raft, sem depender da biblioteca (5.5).
type MsgKind uint8

const (
	MsgApp MsgKind = iota + 1
	MsgAppResp
	MsgVote
	MsgVoteResp
	MsgPreVote
	MsgPreVoteResp
	MsgHeartbeat
	MsgHeartbeatResp
	MsgSnap
	MsgOther
)

// CanSendBeforeSync diz se uma mensagem do Ready pode sair antes do fsync do lote,
// no modo síncrono com F3. Só o MsgApp da líder pode. Todo o resto (em especial
// MsgAppResp e MsgVoteResp) espera o fsync: um MsgAppResp de learner antes do fsync
// faria o gate contar uma durabilidade da Azure que não existe (risco 34).
func CanSendBeforeSync(isLeader bool, k MsgKind) bool {
	return isLeader && k == MsgApp
}

// SplitBeforeSync separa as mensagens de um Ready em "antes do fsync" e "depois do
// fsync", mantendo a ordem dentro de cada grupo.
func SplitBeforeSync[M any](isLeader bool, msgs []M, kind func(M) MsgKind) (before, after []M) {
	for _, m := range msgs {
		if CanSendBeforeSync(isLeader, kind(m)) {
			before = append(before, m)
		} else {
			after = append(after, m)
		}
	}
	return before, after
}

package domain

// Regras de retenção dos dois logs (ADR-055, 6.9). São funções puras para o modelo TLA+
// e os testes de propriedade cobrirem a mesma regra que o código usa.

// RaftCompactLimit devolve até onde o log do Raft pode ser compactado.
//
//   - Segurança: nunca além de durable, o índice coberto pelo último checkpoint (fsync)
//     do log de replicação. Uma entrada apenas copiada some numa queda de host.
//   - Desempenho: segura uma folga para o par mais atrasado (seguidor ou learner) enquanto
//     o atraso dele couber em retainMax entradas. Assim um atraso curto do link não joga o
//     learner no caminho de snapshot do etcd-io/raft. Acima da folga, o par faz catch-up
//     pelo log de replicação e não segura mais o Raft.
func RaftCompactLimit(durable, minPeerMatch Index, retainMax uint64) Index {
	limit := durable
	if minPeerMatch < limit && uint64(limit-minPeerMatch) <= retainMax {
		limit = minPeerMatch
	}
	return limit
}

// ReplogTruncateLimit devolve até onde o log de replicação pode ser truncado (prefixo).
//
//   - Nunca além do que a célula já aplicou no banco (o replay precisa das entradas).
//   - Nunca além do índice durável da maioria Azure, enquanto a Azure estiver dentro da
//     janela (teto de 70%, ADR-041). Fora da janela, ela volta por rebuild e não segura.
//   - Nunca alcança F de um rebuild em curso (ADR-047): o log fica retido desde F.
//
// rebuildFrom = 0 significa nenhum rebuild em curso.
func ReplogTruncateLimit(applied, azureDurable Index, azureInWindow bool, rebuildFrom Index) Index {
	limit := applied
	if azureInWindow {
		limit = min(limit, azureDurable)
	}
	if rebuildFrom > 0 {
		limit = min(limit, rebuildFrom-1)
	}
	return limit
}

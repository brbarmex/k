package domain

// Regra de retenção do log do Raft (6.9). É uma função pura para o modelo TLA+ e os testes
// de propriedade cobrirem a mesma regra que o código usa.

// RaftCompactLimit devolve até onde o log do Raft pode ser compactado.
//
//   - Nunca além do que a célula já aplicou no banco: o replay da partida precisa das
//     entradas ainda não aplicadas, e as aplicadas já estão no banco.
//   - Nunca além do índice durável da maioria Azure, enquanto a Azure estiver dentro da
//     janela (teto de 70%, ADR-041). Fora da janela, ela volta por reconstrução em tabela
//     sombra e não segura o log.
//   - Nunca alcança F de uma reconstrução em curso (ADR-047): o log fica retido desde F.
//   - Desempenho: segura uma folga para o votante mais atrasado enquanto o atraso dele
//     couber em retainMax entradas. Acima da folga, o par não segura mais o log.
//
// rebuildFrom = 0 significa nenhuma reconstrução em curso.
func RaftCompactLimit(applied, azureDurable Index, azureInWindow bool, rebuildFrom, minPeerMatch Index, retainMax uint64) Index {
	limit := applied
	if azureInWindow {
		limit = min(limit, azureDurable)
	}
	if rebuildFrom > 0 {
		limit = min(limit, rebuildFrom-1)
	}
	if minPeerMatch < limit && uint64(limit-minPeerMatch) <= retainMax {
		limit = minPeerMatch
	}
	return limit
}

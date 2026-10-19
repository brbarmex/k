# CE-07 · Atualização de versão em rolagem nas três células

| Campo | Valor |
|---|---|
| Categoria | Caminho eventual (complemento) |
| Origem | Complemento: é a troca de líder mais frequente em produção |
| Requisitos | HA, RPO = 0 |
| Suporte na POC hoje | **Parcial.** Cada célula reinicia sem perder dados; sem transferência planejada, reiniciar a líder custa uma eleição. O formato da entrada no log é JSON sem versão de esquema: duas versões com formatos diferentes no mesmo cluster não são tratadas. |
| Injeção | trocar a imagem nas 3 células, uma de cada vez, followers primeiro, líder por último, esperando a anterior alcançar. **Entre o commit `c9f42af` e o atual não há rolagem:** o formato do WAL mudou (identidade `{node_id, cluster_id}`, RT-20) e a versão nova recusa volumes antigos (FL-06 variante c); essa troca exige PVCs novos. Rodar este cenário entre duas versões com o mesmo formato. |
| Duração | toda a rolagem, com CF-03 |

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | No máximo uma eleição (a da líder) | `dr_leader_changes_total` |
| A3 | Indisponibilidade total medida | RTO-08 somado na rolagem |
| A4 | Versões visíveis | M-16 (`dr_build_info`) mostra a transição célula a célula |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por fase da rolagem.
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-1.
- Além: B-K8S, M-05 (tempo de partida), RTO-07 por célula.

## Lacunas

M-16; transferência planejada (CE-01 a).

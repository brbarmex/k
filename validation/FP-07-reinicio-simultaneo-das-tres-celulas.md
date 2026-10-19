# FP-07 · Reinício simultâneo das três células (partida a frio)

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento: acontece em manutenção do EKS, troca de configuração em massa ou queda de energia |
| Requisitos | RPO = 0 (nada confirmado se perde com todas reiniciando), HA (tempo até voltar) |
| Suporte na POC hoje | **Sim.** Cada célula relê o WAL (com fsync, o que foi confirmado está em pelo menos 2) e o progresso do applier. |
| Injeção | `aws:eks:pod-delete` nas 3 ao mesmo tempo, variantes gracioso e SIGKILL |
| Duração | 10 min antes, reinício, 10 min depois |

## Análise crítica

- É o teste mais direto do fsync: se o WAL perdeu algo confirmado em duas células, aparece aqui.
- O tempo de volta é o **maior** tempo de partida entre as duas primeiras células a ficarem prontas. Desde o Incremento A, a partida relê só a cauda desde o último snapshot e não cresce com o histórico (CA-22): medir com WAL pequeno e depois de 1 h de carga, e os dois devem ficar próximos.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Volta | RTO-04 registrado; decomposição por M-05 |
| A3 | Índices após a volta | `dr_commit_index` de cada célula ≥ o maior índice confirmado no ledger antes de T0 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK antes/depois; M-05 (replay do WAL) por célula.
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-5 na partida.
- Além: B-RAFT-1, B-IDX, B-K8S, RTO-04, RTO-07.

## Lacunas

M-05, M-06.

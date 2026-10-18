# FP-10 · Falha dupla: uma follower fora e a líder cai

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento |
| Requisitos | RPO = 0 (principal); HA não é esperada até uma das duas voltar |
| Suporte na POC hoje | **Sim, como perda de quórum.** |
| Injeção | FP-04 numa follower e, 2 min depois, FP-03 (variante b) na líder |
| Duração | até as duas voltarem + 10 min |

## Análise crítica

- Com a follower A fora, toda confirmação depende da líder **e** da follower B. Quando a líder cai, B fica sozinha e nada é confirmado. Quando A volta, ela não pode ser eleita se o log de B for mais completo; B é eleita com o voto de A. O que estava confirmado está em B (fsync): RPO = 0 se o fsync é honesto.
- É o cenário que mostra por que o tempo de reconvergência de FP-04 é "tempo em risco".

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Nenhum 200 sem maioria | ledger |
| A3 | Quem foi eleita e por quê | `dr_raft_term`, `dr_commit_index` de A e B no instante da eleição |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK antes/depois.
- Golden signals: G-TRF-2, G-ERR-1, G-ERR-2, G-ERR-5.
- Além: B-RAFT-1, B-RAFT-4 (votos recusados por log menos completo), B-IDX, RTO-04, RTO-08.

## Lacunas

M-14.

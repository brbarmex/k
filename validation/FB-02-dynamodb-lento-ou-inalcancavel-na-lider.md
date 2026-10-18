# FB-02 · DynamoDB lento ou inalcançável na célula líder

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do banco (complemento) |
| Origem | Complemento |
| Requisitos | HA, RPO = 0 |
| Suporte na POC hoje | **Sim, com perda de disponibilidade por desenho.** O ACK espera o apply no banco da líder: com o banco dela parado, toda escrita vira PENDING até o atraso passar de `APPLY_LAG_MAX`; a partir daí, 400 `ThrottlingException` com `causa="atraso_apply"`. **A liderança não muda**, porque o Raft não sabe do banco. |
| Injeção | variantes a e b de FB-01 na célula da líder |
| Duração | 10 min antes, 2 min e 10 min de falha, até alcançar + 10 min |

## Análise crítica

- É um ponto de projeto a discutir: o sistema tem 2 bancos saudáveis e um log confirmado, mas **não aceita escrita**, porque o ACK está amarrado ao banco da líder. Não há troca de liderança automática por banco ruim. Para o produto, o RTO desse caso é "até alguém agir" (transferência manual, que a POC não tem).
- As escritas que entraram no log antes do teto viram PENDING, mas **estão confirmadas** (2/3 no WAL) e serão aplicadas na volta: INCERTAS que aparecem nas 3 tabelas.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 (INCERTAS presentes nas 3) |
| A2 | Termo não muda | `dr_raft_term` |
| A3 | Recusa segura após o teto | 400 `atraso_apply`, sem 500, depois de `dr_apply_lag ≥ dr_apply_lag_max` |
| A4 | Indisponibilidade medida | RTO-08 = duração da falha + alcance até abaixo do teto |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK (as PENDING chegam ao prazo), L-APPLY da líder, L-HTTP.
- Golden signals: G-TRF-2 (cai a 0), G-ERR-1 (`pending`), G-ERR-3 (`atraso_apply`), G-SAT-2 da líder.
- Além: B-DRENO da líder, M-07, `dr_commit_undelivered`.

## Lacunas

M-07; troca de liderança por banco ruim (decisão de produto).

# CE-06 · Líder antiga volta com entradas que nunca foram confirmadas

| Campo | Valor |
|---|---|
| Categoria | Caminho eventual (complemento) |
| Origem | Complemento |
| Requisitos | RPO = 0 (nada não confirmado aparece; nada confirmado some) |
| Suporte na POC hoje | **Sim** (truncamento do log pelo Raft). Precisa ser provado com dados, porque o apply e o progresso do applier ficam fora do Raft. |
| Injeção | isolar a líder das duas followers (`aws:eks:pod-network-blackhole-port` na porta gRPC, ou security group) **mantendo o tráfego chegando nela**; depois de a maioria eleger outra líder e confirmar novas escritas, desfazer o isolamento |
| Duração | 10 min antes, 2 min isolada, 10 min depois |

## Objetivo

Provar que as entradas que a líder isolada gravou no próprio WAL (sem maioria) são descartadas na volta, nunca chegam a nenhum banco e nunca receberam 200.

## Análise crítica

- O applier só aplica até o gate, e o gate da líder é o commit do Raft: a cauda não confirmada não chega ao banco. Se algum dia o apply passar à frente do gate (bug), este é o cenário que mostra.
- As requisições da cauda descartada terminam como PENDING (500) no prazo: são INCERTAS que, aqui, **devem estar ausentes** das 3 tabelas.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Nenhuma INCERTA da líder isolada aparece | ORC-04 R3 com presença = 0 para as requisições feitas à líder isolada após o isolamento |
| A2 | Nenhum 200 da líder isolada após o isolamento | ledger |
| A3 | Convergência após a volta | ORC-04 R4; `dr_commit_index` da antiga alcança a nova |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por célula de entrada (as presas na isolada chegam a 7,5 s).
- Golden signals: G-ERR-1 (`pending` na isolada), G-SAT-1 na isolada.
- Além: B-RAFT-1, B-INV-1, B-INV-2, B-IDX da antiga (commit, gate e aplicado não passam do que a nova confirmou).

## Lacunas

M-14.

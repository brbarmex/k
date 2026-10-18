# FC-01 · Chave quente (muitas escritas na mesma pk)

| Campo | Valor |
|---|---|
| Categoria | Carga (complemento) |
| Origem | Complemento |
| Requisitos | RPO = 0 com ordem por chave, HA |
| Suporte na POC hoje | **Sim.** O apply é paralelo por chave e em ordem dentro da chave (ADR-058): a chave quente vira uma fila serial no applier. |
| Injeção | CRG-4 (20% das escritas numa pk) com CF-03 |
| Duração | 2 + 10 min |

## Análise crítica

- O oráculo de pk única não serve aqui. Para a chave quente, o critério é: o valor final nas 3 tabelas é o da escrita confirmada com **maior** `x-dr-index`, e toda leitura forte vê valores em ordem não decrescente de versão (ORC-06).
- A fila serial da chave quente limita a vazão do apply daquela chave à latência de um PutItem: se a taxa da chave passar disso, o atraso cresce e a admissão recusa **todas** as escritas, não só as da chave quente.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Valor final correto nas 3 | reconciliador: `_dr_v` final = maior `x-dr-index` confirmado da pk |
| A2 | Leituras em ordem | ORC-06 |
| A3 | Efeito no resto | p99 de L-ACK das outras chaves ≤ 1,2 × baseline; G-ERR-3 = 0 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK separado (chave quente × demais), L-APPLY.
- Golden signals: G-TRF-2, G-ERR-3 (`atraso_apply`), G-SAT-2, G-SAT-3 (`dr_apply_inflight`).
- Além: B-DRENO; CloudWatch do DynamoDB (partição quente).

## Lacunas

M-01.

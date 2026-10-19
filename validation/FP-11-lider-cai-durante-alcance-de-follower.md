# FP-11 · Líder cai enquanto uma follower ainda está alcançando

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento |
| Requisitos | RPO = 0, HA |
| Suporte na POC hoje | **Sim.** |
| Injeção | FP-04 (10 min fora); na volta, quando `dr_peer_lag` da follower estiver na metade, derrubar a líder |
| Duração | até reconvergência + 10 min |

## Análise crítica

- **Limite do Incremento A (`eec02c9`):** se, enquanto a follower que estava alcançando está fora, a líder passar do teto de RAM (`RAFT_RAM_MAX`) ou mudar de líder, a follower que estava alcançando volta atrás da cauda e fica parada em `raft.atrasCauda` (FL-02) até a recuperação do Incremento B. Com CRG-1 e quedas curtas isso não acontece; com CRG-2 por minutos, sim.
- Diferente de FP-10: aqui as duas que restam estão vivas, mas uma tem o **log** incompleto (não só o apply). O Raft só elege a de log mais completo, que é a follower que nunca saiu; a que estava alcançando passa a alcançar dela.
- Se a que estava alcançando tem o log completo mas o **apply** atrasado e for eleita, cai em CE-05.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | A eleita tem o log mais completo | `dr_commit_index` das duas no instante da eleição |
| A3 | Delta-T | RTO-02..05 |
| A4 | Alcance continua a partir da nova líder | `dr_peer_lag` na nova líder |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK, L-APPLY e L-REPL das duas sobreviventes.
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-2.
- Além: B-RAFT-1, B-RAFT-3, B-RAFT-4, B-DRENO, RTO-01..08.

## Lacunas

M-11.

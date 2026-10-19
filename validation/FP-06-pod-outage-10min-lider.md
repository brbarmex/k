# FP-06 · [pod-outage-10min] Queda da célula líder por 10 minutos

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha |
| Origem | Lista do Bruno |
| Requisitos | RPO = 0, FT, HA |
| Suporte na POC hoje | **Sim.** |
| Injeção | `replicas=0` no cluster da líder por 10 min |
| Duração | 10 min antes, 10 min fora, até reconvergência + 10 min |

## Análise crítica

- **Limite do Incremento A (`eec02c9`):** se, enquanto a antiga líder está fora, a líder passar do teto de RAM (`RAFT_RAM_MAX`) ou mudar de líder, a antiga líder volta atrás da cauda e fica parada em `raft.atrasCauda` (FL-02) até a recuperação do Incremento B. Com CRG-1 e quedas curtas isso não acontece; com CRG-2 por minutos, sim.
- Os primeiros segundos são FP-03. O que é próprio deste cenário: (1) por 10 min o sistema roda com **2 células**, sem margem para mais uma falha; (2) a antiga líder volta com uma cauda não confirmada que precisa ser descartada (CE-06) e com 10 min de apply para alcançar (FP-04).
- A nova líder herda o atraso de apply que tinha como follower (CE-05): o Delta-T pode ser maior que o da eleição.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04; INCERTAS da antiga líder ausentes ou presentes nas 3 |
| A2 | Delta-T | RTO-02..05, decomposto |
| A3 | 10 min com 2 células sem degradação | p99 de L-ACK ≤ 1,2 × baseline após T_estável |
| A4 | Volta e reconvergência | como FP-04 A3 e A4, aplicado à antiga líder; ela fica follower |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK em quatro janelas (antes, eleição, 2 células, volta); L-APPLY da nova líder e da antiga na volta; L-REPL.
- Golden signals: G-TRF-1..3, G-ERR-1..5, G-SAT-2, G-SAT-3, G-SAT-5.
- Além: B-RAFT-1..4, B-INV-1..3, B-DRENO, B-K8S, RTO-01..08.

## Lacunas

M-04, M-11, M-14.

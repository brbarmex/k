# CF-01 · Replicação entre as células com tráfego chegando na líder

| Campo | Valor |
|---|---|
| Categoria | Caminho feliz |
| Origem | Lista do Bruno |
| Requisitos | RPO = 0 (principal), base de comparação para FT e HA |
| Suporte na POC hoje | **Sim.** `WriteService.Write` propõe na líder, espera o commit 2/3 com fsync, o gate e o apply local (`internal/usecase/write.go`). |
| Injeção | nenhuma |
| Duração | 2 min de aquecimento + 10 min de medição, para CRG-1 e depois CRG-2 |

## Objetivo

Provar que, sem falha, toda escrita confirmada pela líder chega aos 3 bancos com o mesmo valor e a mesma versão, e medir o **baseline** de latência, vazão e recursos que todos os outros cenários usam como referência.

## Análise crítica

- O cenário está bem definido, mas "replicação dos dados entre as células" precisa de critério objetivo: aqui ele é o oráculo (ORC-04), não o painel de índices.
- O ACK da POC espera o apply **no DynamoDB da líder** (`Applied.WaitApplied`). Então a latência do ACK inclui uma escrita no banco; o SLO de 50 ms do produto (ADR-036) exclui o banco. Os dois números precisam ser separados (M-01), senão o baseline mistura Raft e DynamoDB.
- As followers aplicam de forma assíncrona depois do gate. A confirmação ao cliente **não** garante que o item já esteja visível na tabela da follower; garante que está no WAL de 2 das 3 células. O cenário mede quanto tempo a follower leva (L-REPL), que é o que importa para uma leitura direta na tabela (bypass) e para o RTO de uma promoção.
- É este cenário que define os números de "baseline" dos outros. Se ele rodar com outra configuração (AMB-08), todos os outros perdem a base.

## Pré-condições

- AMB-01 a AMB-12 prontos; 3 células prontas (`dr_ready == 1`), uma líder com `dr_quorum_ok == 1`.
- Tabelas vazias ou com pks de prefixo diferente da execução.
- Prometheus coletando a 1 s nas 3 células; reconciliador pronto.

## Passos

1. Descobrir a líder pelo `/status` (ou `dr_is_leader`).
2. Rodar CRG-1 contra a **API da líder**, gravando o ledger (ORC-01).
3. Parar a carga; esperar quiescência (ORC-03).
4. Rodar o reconciliador; publicar `dr_val_*`.
5. Repetir com CRG-2.

## Resultado esperado

Todas as escritas CONFIRMADAS, zero RECUSADA e zero INCERTA em CRG-1. Índices iguais nas 3 células após a quiescência. Termo sem mudança.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 R1 a R5 zerados (`dr_val_*`) |
| A2 | Sem eleição | `increase(dr_leader_changes_total[run]) == 0` e `dr_raft_term` constante nas 3 |
| A3 | Sem erro em CRG-1 | G-ERR-1, G-ERR-2, G-ERR-3 iguais a 0; k6 `http_req_failed == 0` |
| A4 | Convergência | após a carga, `dr_applied_index` igual nas 3 e igual a `dr_commit_index` da líder em até 60 s |
| A5 | Invariantes | B-INV-1 e B-INV-3 sempre verdadeiros |
| A6 | Laço do Raft saudável | p99 de L-TICK ≤ 0,15 s; nenhum ponto acima de 0,5 × timeout de eleição |
| A7 | Recursos | CPU throttling < 1% dos períodos; RSS estável (inclinação registrada, ver CF-06) |

## Métricas

### Latência (registrar p90, p99, p99.9, max)

| Sinal | p90 | p99 | p99.9 | max | Observação |
|---|---|---|---|---|---|
| L-ACK (cliente, por célula de entrada) | registrar | hipótese ≤ 50 ms | hipótese ≤ 100 ms | ≤ 1 s (nenhum perto do prazo de 7,5 s) | **define o baseline** |
| L-ACK-S | registrar | registrar | registrar | M-03 | diferença para L-ACK = rede e balanceador |
| L-ETAPAS (M-01) | registrar | registrar | registrar | M-03 | separa Raft de DynamoDB |
| L-FSYNC | registrar | hipótese ≤ 5 ms em gp3 | registrar | M-03 | comparar com L-EBS |
| L-LOOP | registrar | registrar | registrar | M-03 | |
| L-TICK | ≈ 0,1 s | ≤ 0,15 s | registrar | registrar | |
| L-APPLY (por célula) | registrar | registrar | registrar | M-03 | comparar com L-DB do CloudWatch |
| L-REPL (M-11) por follower | registrar | registrar | registrar | registrar | sem M-11: `dr_apply_lag / vazão do apply` como aproximação |

### Golden signals

- Tráfego: G-TRF-1 e G-TRF-2 (entrada = ACK/s na líder; nas followers, entrada 0).
- Erros: G-ERR-1 a G-ERR-5 iguais a 0.
- Saturação: G-SAT-1 a G-SAT-7, com atenção a `dr_apply_lag` (deve ficar perto de 0) e a `VolumeIOPSExceededCheck` (deve ficar 0 em CRG-1).

### Além dos golden signals

B-RAFT-1 a B-RAFT-5, B-IDX, B-INV-1, B-INV-3, B-APP, B-DRENO (positiva nas followers), B-GO, B-REDE (RTT entre células), B-CUSTO (WCU ≈ 3 × escritas).

## Prova de RPO = 0

ORC-04 completo. Este é o único cenário em que **todas** as escritas devem ser CONFIRMADAS: qualquer INCERTA sem falha injetada é um defeito a investigar.

## Lacunas de observabilidade

M-01 (sem ele não se separa banco de consenso), M-03 (max e p99.9 precisos), M-11.

## Riscos e hipóteses

- A vazão do apply nas followers é o provável gargalo (no LocalStack eram ~90 itens/s por célula; no DynamoDB real deve ser bem maior, mas é limitada por `APPLY_WORKERS / latência do PutItem`). Se B-DRENO ficar negativa em CRG-2, a follower nunca alcança e o cenário falha em A4.

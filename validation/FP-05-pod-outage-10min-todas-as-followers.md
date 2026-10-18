# FP-05 · [pod-outage-10min] Queda de todas as células follower por 10 minutos

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha |
| Origem | Lista do Bruno |
| Requisitos | RPO = 0; **HA não é esperada** (sem maioria por 10 min) |
| Suporte na POC hoje | **Sim, como perda de quórum prolongada.** |
| Injeção | `replicas=0` nos dois clusters das followers por 10 min |
| Duração | 10 min antes, 10 min fora, até reconvergência + 10 min |

## Análise crítica

- Mesmo comportamento de FP-02, com dois agravantes: (1) 10 min de clientes recebendo erro e repetindo (o SDK faz 3 tentativas de até 30 s; depois desiste), e (2) o retorno. A RAM e o disco da sobrevivente **não crescem** nesse período, porque nada é confirmado; mas a fila de requisições esperando líder e as conexões abertas sim.
- Na volta, se as duas followers voltarem juntas, elas podem eleger uma delas antes de a antiga líder participar (ela continua viva). Qualquer uma é válida; o oráculo decide.
- O cenário mostra o que o cliente vê em 10 min de indisponibilidade total: isso é o que o produto quer evitar com a Azure (fora da POC). Serve como dado para a apresentação, não como falha da POC.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Zero 200 na janela sem maioria | ledger |
| A2 | RPO = 0 | ORC-04 (INCERTAS confirmadas depois, contadas) |
| A3 | Sobrevivente estável | sem reinício, `dr_http_inflight` limitado, `process_resident_memory_bytes` sem crescimento anormal |
| A4 | Volta | RTO-04 a partir da volta da maioria; uma líder com quórum |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK antes/depois; L-HTTP das respostas de erro na janela (devem ser ~`WAIT_LEADER`).
- Golden signals: G-TRF-1, G-TRF-2, G-ERR-1, G-ERR-2, G-ERR-5, G-SAT-1, G-SAT-4, G-SAT-5.
- Além: B-RAFT-1, B-RAFT-2, B-K8S, RTO-04, RTO-08.

## Lacunas

M-09.

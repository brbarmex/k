# FT-01 · Salto de relógio numa célula

| Campo | Valor |
|---|---|
| Categoria | Tempo (complemento) |
| Origem | Complemento |
| Requisitos | RPO = 0, HA; e a **confiabilidade da própria medição do RTO** |
| Suporte na POC hoje | **Sim (inferido).** O Raft da POC não depende de relógio de parede: usa ticks (`time.Ticker`, relógio monotônico), `CheckQuorum` e ReadIndex, sem leitura por lease. |
| Injeção | no nó de uma célula, parar o chrony e ajustar o relógio em +5 s, −5 s e +60 s (via `aws:ssm:send-command`), na líder e numa follower |
| Duração | 10 min antes, 10 min com o desvio, 10 min depois |

## Análise crítica

- O app deve passar sem efeito. O que quebra é a **observação**: logs com instante errado e decomposição do RTO entre máquinas (AMB-11). Este cenário mostra o tamanho do erro que um desvio introduz nas medidas dos outros cenários.
- No produto, o lease do modo degradado (ADR-044) depende de deriva de relógio: este cenário vira obrigatório quando houver Azure.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Nenhum efeito no app | termo constante; L-ACK, L-TICK iguais ao baseline |
| A2 | RPO = 0 | ORC-04 |
| A3 | Desvio visível | `node_timex_offset_seconds` |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK, L-TICK.
- Golden signals: G-TRF-2, G-ERR-1..5.
- Além: B-RAFT-1, `node_timex_offset_seconds`.

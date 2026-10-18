# CE-03 · Eleição sem falha real: CPU estrangulada ou laço do Raft lento

| Campo | Valor |
|---|---|
| Categoria | Caminho eventual (complemento) |
| Origem | Complemento: foi a causa das eleições em série no teste de carga de 14/10 |
| Requisitos | HA (eleições falsas derrubam a disponibilidade sem nenhuma falha) |
| Suporte na POC hoje | **Parcial.** O laço do Raft não espera o apply (ADR-060), mas um fsync lento ou CPU estrangulada ainda atrasam o tick e o heartbeat. |
| Injeção | `aws:eks:pod-cpu-stress` na líder (80% e 100%), ou limite de CPU reduzido (`limits.cpu`) e CRG-2 |
| Duração | 10 min antes, 10 min com estresse, 10 min depois |

## Objetivo

Mostrar a partir de que nível de estrangulamento a líder perde a liderança sem ter caído, e se o `ELECTION_TICKS` escolhido tem folga suficiente.

## Análise crítica

- Uma eleição falsa custa um Delta-T inteiro sem nenhum benefício. O critério não é "nunca eleger", é saber o limiar e que o limiar fique acima do uso normal com folga registrada.
- O tick do Raft é um `time.Ticker` de 100 ms dentro do mesmo `select` do Ready: um Ready longo (fsync de 1 s) atrasa todos os ticks daquele intervalo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Limiar documentado | menor nível de estresse com `increase(dr_leader_changes_total) > 0` |
| A2 | Uso normal com folga | no CF-05, L-TICK p99.9 ≤ 30% do timeout de eleição |
| A3 | RPO = 0 mesmo com eleições | ORC-04 |

## Métricas

- Latência (p90, p99, p99.9, max): L-TICK, L-LOOP, L-FSYNC, L-ACK.
- Golden signals: G-TRF-2, G-ERR-1..3, G-SAT-4 (throttling de CPU em % dos períodos), G-SAT-1.
- Além: B-RAFT-1, B-RAFT-4 (`prevote`/`vote`), B-RAFT-5, B-GO (pausas do GC).

## Lacunas

M-15.

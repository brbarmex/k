# FR-04 · Latência e perda de pacotes entre as AZs (faixa cinzenta)

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha de rede (complemento) |
| Origem | Complemento (cenário C25 do pacote: link lento) |
| Requisitos | HA (latência e eleições), RPO = 0 |
| Suporte na POC hoje | **Sim** (nada quebra; tudo fica lento). Não há alerta de faixa cinzenta na POC. |
| Injeção | `aws:eks:pod-network-latency` (10, 50, 200, 500 ms com variação) e `aws:eks:pod-network-packet-loss` (1%, 5%, 20%) na líder e, noutra execução, numa follower |
| Duração | 10 min antes, 10 min por degrau, 10 min depois |

## Análise crítica

- O commit exige 2 de 3: latência numa follower só dói se a outra também estiver lenta. Latência na líder dói sempre.
- Com latência perto do intervalo do heartbeat (100 ms) e perda alta, as followers podem perder heartbeats suficientes para disparar eleição. O cenário encontra esse limiar.
- O RTT entre AZs em São Paulo é tipicamente baixo (ordem de 1 ms); o baseline de CF-01 registra o real.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | L-ACK acompanha o RTT da segunda célula mais rápida | p50 de L-ACK − baseline ≈ latência injetada (na líder) |
| A3 | Limiar de eleição documentado | menor degrau com eleição |

## Métricas

- Latência (p90, p99, p99.9, max) por degrau: L-ACK, L-FWD, L-READ, L-ETAPAS (propose → commit), L-TICK.
- Golden signals: G-TRF-2, G-ERR-1..3, G-SAT-1, G-SAT-3.
- Além: B-RAFT-3 (`dr_peer_lag`), B-RAFT-4 (reenvio de `app`), B-RAFT-5, B-REDE (retransmissões TCP), M-10.

## Lacunas

M-01, M-10; alerta de faixa cinzenta (ADR-037 é do produto).

# FR-03 · Partição assimétrica (a líder envia mas não recebe, ou o contrário)

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha de rede (complemento) |
| Origem | Complemento: falhas parciais de rede são as que mais escapam dos testes |
| Requisitos | RPO = 0, HA |
| Suporte na POC hoje | **Sim no Raft** (`CheckQuorum` cobre "envia e não recebe"); **a provar** no encaminhamento e na barreira de leitura, que usam o mesmo gRPC. |
| Injeção | regra de saída ou de entrada só (NACL ou `iptables` no nó via `aws:ssm:send-command`) entre a líder e as outras, nos dois sentidos em execuções separadas; e um caso "líder alcança só uma das followers" |
| Duração | 10 min antes, 2 min, 10 min depois |

## Análise crítica

- "Líder envia e não recebe": as followers recebem heartbeat e não elegem ninguém, mas a líder não recebe respostas e não confirma nada; com `CheckQuorum`, ela renuncia, e só então as followers elegem. O Delta-T é maior que numa queda limpa.
- "Líder alcança só uma follower": a maioria (líder + uma) segue; a terceira vira candidata repetidamente se não tiver `PreVote`. Com `PreVote`, deve ficar quieta.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Sem eleições em série | `increase(dr_leader_changes_total)` ≤ 1 por execução |
| A3 | Delta-T medido por sentido | RTO-02..04 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK, L-FWD (M-02), L-READ.
- Golden signals: G-TRF-2, G-ERR-1..5.
- Além: B-RAFT-1..5, `dr_peer_send_dropped_total`, M-10.

## Lacunas

M-02, M-10.

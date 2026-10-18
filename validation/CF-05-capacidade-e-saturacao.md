# CF-05 · Capacidade e saturação (degraus até a contrapressão)

| Campo | Valor |
|---|---|
| Categoria | Caminho feliz no limite (complemento) |
| Origem | Complemento: sem o teto medido, CRG-1 e CRG-2 não têm valor |
| Requisitos | RPO = 0 sob sobrecarga, HA (degradar recusando, não perdendo) |
| Suporte na POC hoje | **Sim.** Contrapressão por `buffer_cheio` (MaxUncommittedEntriesSize 32 MiB), `atraso_apply` (APPLY_LAG_MAX) e `nao_lider`, todas como 400 `ThrottlingException` antes de entrar no log. |
| Injeção | nenhuma; só carga crescente |
| Duração | degraus de 3 min: 100, 300, 600, 1000, 1500, 2000, 3000, 4000 TPS (ajustar), com tráfego CF-03 |

## Objetivo

Encontrar a **vazão máxima sustentada** (maior degrau com p99 estável e sem contrapressão) e provar que, acima dela, a POC recusa com ThrottlingException em vez de perder ou deixar escritas INCERTAS.

## Análise crítica

- A admissão olha só o atraso do apply **da líder**. Uma follower com apply mais lento que a entrada acumula atraso sem limite e sem recusar nada: a escrita continua confirmada (2/3 no WAL), mas a follower nunca alcança. Por isso o critério de capacidade inclui B-DRENO de **todas** as células, não só a ausência de throttling.
- O gargalo pode estar no fsync (IOPS do gp3), no apply (DynamoDB), na CPU (JSON e gRPC) ou no laço do Raft. O cenário precisa apontar qual, senão o número não se transfere ao produto.
- O k6 com taxa de chegada constante continua mandando quando a POC desacelera: é o que se quer (modelo aberto). Registrar `dropped_iterations` do k6, que indica que o próprio gerador saturou.

## Passos

1. Para cada degrau: 3 min de carga CF-03, registrar tudo.
2. Parar no primeiro degrau em que qualquer critério de "sustentado" falhar, e rodar mais um degrau acima para observar a recusa.
3. Quiescência e oráculo ao final.

## Critérios

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Degrau sustentado | p99 de L-ACK ≤ 2 × baseline; G-ERR-3 = 0; B-DRENO ≥ 0 nas 3 células; L-TICK p99 ≤ 0,15 s |
| A2 | Acima do teto, só recusa segura | 500 e timeouts = 0; o excesso aparece como 400 ThrottlingException com a causa em `dr_throttled_total` |
| A3 | RPO = 0 | ORC-04 (nenhuma RECUSADA_SEGURA presente nos bancos) |
| A4 | Gargalo identificado | qual saturou primeiro: `VolumeIOPSExceededCheck`, L-FSYNC, `dr_apply_inflight / dr_apply_workers`, G-SAT-4, `ThrottledRequests` do DynamoDB |
| A5 | Sem eleição por carga | `dr_leader_changes_total` sem aumento |

## Métricas

- Latência (p90, p99, p99.9, max) **por degrau**: L-ACK, L-ACK-S, L-FSYNC, L-LOOP, L-TICK, L-APPLY, L-ETAPAS.
- Golden signals por degrau: G-TRF-1..3, G-ERR-1..5, G-SAT-1..7.
- Além: B-DRENO por célula, B-RAFT-3 (`dr_peer_lag`), `dr_commit_undelivered`, B-GO, B-CUSTO, `dropped_iterations` do k6.

## Saída

Tabela "degrau × sinal" e o valor de **vazão máxima sustentada** que alimenta CRG-1 e CRG-2.

## Lacunas

M-01 (onde o tempo vai), M-07 (erros do DynamoDB no apply).

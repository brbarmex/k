# FD-04 · Disco lento ou no teto de IOPS do gp3

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento: "fsync é o gargalo" é decisão do projeto; este cenário mede o quanto |
| Requisitos | HA (latência e eleições falsas), RPO = 0 |
| Suporte na POC hoje | **Sim** (nada quebra; tudo fica mais lento). |
| Injeção | **a** vizinho barulhento: `fio` no mesmo volume, a partir de um contêiner de depuração, consumindo o IOPS provisionado; **b** pausas curtas repetidas com `aws:ebs:pause-volume-io` (ex.: 1 s a cada 10 s) para criar caudas de p99.9; **c** ação de latência de I/O do EBS no FIS, se existir na conta (confirmar no catálogo do FIS); **d** `aws:eks:pod-io-stress` (confirmar se atinge o PVC ou só o disco raiz) |
| Duração | 10 min antes, 10 min com a falha, 10 min depois; numa follower e na líder |

## Análise crítica

- O gp3 não tem crédito de burst: acima de 3000 IOPS (ou do provisionado), a fila cresce e a latência sobe. `VolumeIOPSExceededCheck` do CloudWatch mostra isso, mas com granularidade de 1 min.
- Na líder, um fsync lento atrasa o commit e todos os ACKs; acima do timeout de eleição, vira eleição (CE-03). Numa follower, só tira a folga: a líder segue com a outra.
- Este é o cenário que dá o número para dimensionar o gp3 (IOPS e throughput provisionados) do produto.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Correlação fsync → ACK | aumento de p99 de L-FSYNC da líder reflete em L-ACK |
| A3 | Follower lenta não afeta | p99 de L-ACK ≤ 1,2 × baseline com a follower lenta |
| A4 | Limiar de eleição | menor latência de fsync que causa eleição (com CE-03) |

## Métricas

- Latência (p90, p99, p99.9, max): L-FSYNC, L-EBS, L-LOOP, L-TICK, L-ACK, L-ETAPAS (propose → commit).
- Golden signals: G-TRF-2, G-ERR-3 (`buffer_cheio`, se o log não persistido acumular), G-SAT-6 (`VolumeQueueLength`, `VolumeIOPSExceededCheck`, `VolumeThroughputExceededCheck`, `node_disk_io_time_seconds_total`).
- Além: B-RAFT-1, B-RAFT-3, M-15.

## Lacunas

M-01, M-15.

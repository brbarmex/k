# FL-01 · Compactação do log sob carga: RAM, disco e partida estáveis

| Campo | Valor |
|---|---|
| Categoria | Caminho feliz do log (complemento) |
| Origem | Complemento: é a CA-21, a CA-22 e a CA-27 da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md) rodadas na AWS |
| Requisitos | HA (o processo não cai por memória nem por disco), RPO = 0 |
| Suporte na POC hoje | **Sim** (Incremento A, commit `eec02c9`). O `Compactor` grava o snapshot-marcador a cada `SNAPSHOT_EVERY_ENTRIES` ou `SNAPSHOT_EVERY_BYTES`, compacta a RAM até `snapshot − RAFT_CATCHUP_ENTRIES` e libera o disco até `min(aplicado com fsync, piso, pinos, snapshot)`. |
| Injeção | nenhuma; só carga e reinícios programados |
| Duração | 1 h com CRG-1 (perfil CF-03); uma rodada de 10 min com CRG-2 |

## Objetivo

Provar que, com carga contínua, a memória do log e o disco do WAL ficam limitados nas 3 células, que o snapshot não atrasa o laço do Raft e que o tempo de partida não cresce com o total escrito.

## Análise crítica

- O snapshot custa um fsync extra no WAL e espera o mutex do WAL contra o `wal.Save` do laço (SG-22). Em gp3 isso aparece como um pico de L-LOOP a cada snapshot. O critério é que o pico não chegue perto do timeout de eleição.
- O disco só é liberado até o **progresso com fsync**: uma follower com o apply atrasado segura o próprio disco (não o das outras). Por isso o critério de disco é por célula.
- O piso que a líder publica depende do `Match` das followers **dentro da janela**. Com as 3 saudáveis, o piso acompanha o commit e o WAL retido fica em poucos segmentos.
- O tempo de partida passa a depender só da cauda desde o último snapshot (até 10.000 entradas no padrão), e não do histórico.

## Pré-condições

- AMB-08 com as variáveis da compactação registradas (padrões da S.6: `SNAPSHOT_EVERY_ENTRIES=10000`, `SNAPSHOT_EVERY_BYTES=64MiB`, `RAFT_CATCHUP_ENTRIES=5000`, `RAFT_RAM_MAX=256MiB`, `RETAIN_MAX_BYTES=2GiB`, `WAL_SEGMENT_BYTES=64000000`), `GOMEMLIMIT` em ~80% do limite de memória do pod (AP-15) e PVC maior que `RETAIN_MAX_BYTES / RETAIN_DISK_HARD` (a partida recusa o contrário).
- `CLUSTER_ID` igual nas 3 células.

## Passos

1. CF-03 com CRG-1 por 1 h.
2. A cada 15 min, reiniciar uma follower (FP-01 variante a) e registrar a duração da partida.
3. Rodada de 10 min com CRG-2.
4. Quiescência; oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Snapshot avança nas 3 | `/status` `raft.snapshot` crescendo; `rate(dr_raft_snapshot_duration_seconds_count[5m]) > 0` |
| A3 | RAM limitada | `/status` `raft.memoria.entradas ≤ SNAPSHOT_EVERY_ENTRIES + RAFT_CATCHUP_ENTRIES` e `raft.memoria.bytes ≤ RAFT_RAM_MAX` o tempo todo |
| A4 | Memória do processo estável | `process_resident_memory_bytes` sem crescer mais de 10% entre janelas de 10 min (critério da CA-27) |
| A5 | Disco limitado | `/status` `wal.retido.bytes` estável; `kubelet_volume_stats_used_bytes` sem tendência de alta |
| A6 | Snapshot não derruba a liderança | `increase(dr_leader_changes_total) == 0`; p99.9 de L-LOOP < 30% do timeout de eleição |
| A7 | Partida constante | `dr_startup_duration_seconds` da 1ª e da última reinicialização dentro de ±50%; log `partida: WAL relido` com `entradas ≤ SNAPSHOT_EVERY_ENTRIES + backlog` |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK, L-LOOP (com e sem snapshot no intervalo), L-FSYNC, `dr_raft_snapshot_duration_seconds`.
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-5, G-SAT-6.
- Além: B-RAFT-1, B-GO (pausas de GC perto do `GOMEMLIMIT`), `wal.piso` e `wal.janela` da líder, `dr_startup_duration_seconds`.

## Prova de RPO = 0

ORC-04. A compactação não muda o que é confirmado; o oráculo prova que nada se perdeu ao apagar segmentos.

## Lacunas

M-04 (hoje os números estão no `/status`; as séries `dr_raft_mem_bytes`, `dr_raft_mem_entries`, `dr_raft_snapshot_index`, `dr_wal_retained_bytes` e `dr_wal_files` só existem no Incremento B, O-42). O painel precisa ler o `/status` até lá.

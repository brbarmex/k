# CF-06 · Soak de longa duração (memória e disco com a compactação do log)

| Campo | Valor |
|---|---|
| Categoria | Caminho feliz no tempo (complemento) |
| Origem | Complemento |
| Requisitos | HA (o processo não pode cair sozinho), RPO = 0 |
| Suporte na POC hoje | **Sim** (Incremento A, commit `eec02c9`). O `Compactor` grava um snapshot-marcador a cada 10.000 entradas ou 64 MiB, compacta a RAM do log (teto `RAFT_RAM_MAX`) e libera os segmentos do WAL até o aplicado com fsync; o processo roda com `GOMEMLIMIT` abaixo do limite do pod. Até o commit `c9f42af` a POC não compactava, e este cenário existia para provar a queda. |
| Injeção | nenhuma; só tempo |
| Duração | 4 h com CRG-1 (perfil CF-03) |

## Objetivo

Provar que a memória e o disco ficam estáveis por horas com a carga real e que o tempo de partida não cresce com o tempo de serviço. É a versão na AWS da CA-27 da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md), que roda local por 1 h a 10 TPS (`task soak`); aqui a carga é CRG-1 e, se CF-05 sustentar, uma hora de CRG-2.

## Análise crítica

- Com as 3 células saudáveis, o piso de retenção acompanha o commit e o WAL retido fica em poucos segmentos de 64 MB. A RAM do log fica em até `SNAPSHOT_EVERY_ENTRIES + RAFT_CATCHUP_ENTRIES` entradas.
- A memória do **processo** inclui mais que o log: a fila do applier (até 64 lotes de 1 MiB), as filas por par (`PEER_QUEUE_BYTES`, 64 MiB cada) e o heap do gRPC. O critério é de tendência, não de valor absoluto.
- O GC trabalha mais perto do `GOMEMLIMIT`: um `GOMEMLIMIT` mal escolhido aparece como pausas de GC e atraso do tick, não como OOM.
- Uma follower com o apply atrasado segura o **próprio** disco (só o aplicado com fsync libera); com o banco saudável isso não deve acontecer.

## Passos

1. CRG-1 contínuo (perfil CF-03) por 4 h.
2. A cada 30 min: reiniciar uma follower (FP-01) e medir o tempo de partida.
3. Se CF-05 sustentar CRG-2, mais 1 h com CRG-2.

## Critérios de aceite

| # | Critério | Fonte |
|---|---|---|
| A1 | RSS estável | `avg_over_time(process_resident_memory_bytes[10m])` sem crescer mais de 10% entre janelas (critério da CA-27) |
| A2 | Disco estável | `kubelet_volume_stats_used_bytes` e `/status` `wal.retido.bytes` sem tendência de alta |
| A3 | RAM do log limitada | `/status` `raft.memoria` (M-04) dentro dos limites da S.6 |
| A4 | Partida constante | `dr_startup_duration_seconds` (M-05) da primeira e da última reinicialização dentro de ±50% |
| A5 | Latência estável | L-ACK p99 por hora dentro de ±20% do baseline; `go_gc_duration_seconds` sem tendência |
| A6 | Nenhum OOM | `kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}` ausente |
| A7 | RPO = 0 | ORC-04 |

## Métricas

- Latência (p90, p99, p99.9, max) **por hora**: L-ACK, L-FSYNC, L-LOOP, L-TICK, L-APPLY, `dr_raft_snapshot_duration_seconds`.
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-5 (memória), G-SAT-6 (disco).
- Além: M-04, M-05, B-GO, B-K8S.

## Lacunas

M-04 só tem série Prometheus no Incremento B (O-42); até lá, coletar o `/status`.

## Decisão que este cenário alimenta

Os valores de produção de `SNAPSHOT_EVERY_*`, `RAFT_RAM_MAX`, `GOMEMLIMIT` e do tamanho do PVC.

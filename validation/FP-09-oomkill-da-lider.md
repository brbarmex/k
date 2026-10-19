# FP-09 · OOMKill da célula líder

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento |
| Requisitos | FT, HA, RPO = 0 |
| Suporte na POC hoje | **Sim como queda** (igual a SIGKILL). Desde o Incremento A (`eec02c9`) a RAM do log tem teto (`RAFT_RAM_MAX`) e o processo roda com `GOMEMLIMIT`: o OOM natural da variante b **não deve acontecer** com o limite de memória coerente com o orçamento (AP-15). Até o commit `c9f42af`, o log inteiro ficava em memória e as 3 células chegavam ao OOM perto do mesmo instante. |
| Injeção | **a** `aws:eks:pod-memory-stress` na líder até o limite; **b** natural: limite de memória baixo + CRG-2 até o OOM |
| Duração | até o OOM + 10 min |

## Análise crítica

- Variante a mede um OOM isolado (≈ FP-03 variante b).
- Variante b agora prova o contrário do que provava antes: com CRG-2 e o limite de memória no valor de AMB-04, a memória do processo deve estabilizar abaixo do limite (o log é compactado e o GC trabalha perto do `GOMEMLIMIT`). Para forçar o OOM, é preciso um limite **abaixo** do orçamento da AP-15 (`RAFT_RAM_MAX` + 32 MiB de não commitadas + `PEER_QUEUE_BYTES` × pares + fila do applier + folga); registrar o limite usado.
- Ao voltar, a célula relê só a cauda desde o último snapshot: a partida não recoloca o histórico na RAM.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Variante b com o limite de AMB-04: nenhum OOM | `kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}` ausente; `process_resident_memory_bytes` estável; `/status` `raft.memoria.bytes ≤ RAFT_RAM_MAX` |
| A3 | Célula volta com folga de memória | RSS após a partida ÷ limite |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK; `go_gc_duration_seconds` (o GC aperta perto do limite e atrasa o laço).
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-5.
- Além: M-04, B-GO, B-K8S, RTO-04, RTO-07.

## Lacunas

M-04.

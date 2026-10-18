# FP-09 · OOMKill da célula líder

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento: consequência direta de CF-06 (log inteiro em memória) |
| Requisitos | FT, HA, RPO = 0 |
| Suporte na POC hoje | **Sim como queda** (igual a SIGKILL); **o risco** é que as 3 células cresçam no mesmo ritmo e o OOM chegue a todas perto do mesmo instante. |
| Injeção | **a** `aws:eks:pod-memory-stress` na líder até o limite; **b** natural: limite de memória baixo + CRG-2 até o OOM |
| Duração | até o OOM + 10 min |

## Análise crítica

- Variante a mede um OOM isolado (≈ FP-03 variante b). Variante b mede o que acontece de verdade: se as 3 têm o mesmo limite e o mesmo log, a segunda e a terceira estão perto do OOM quando a primeira cai. Ao voltar, a primeira relê o WAL inteiro para a RAM e volta a ficar perto do limite.
- O cenário prova a necessidade de compactação (ADR-063) com um evento visível no painel.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Intervalo entre OOMs das 3 (variante b) | `kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}` com instante |
| A3 | Célula volta com folga de memória | RSS após a partida ÷ limite |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK; `go_gc_duration_seconds` (o GC aperta perto do limite e atrasa o laço).
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-5.
- Além: M-04, B-GO, B-K8S, RTO-04, RTO-07.

## Lacunas

M-04.

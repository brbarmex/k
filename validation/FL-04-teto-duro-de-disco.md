# FL-04 · Teto duro do disco do log

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do log (complemento) |
| Origem | Complemento: é a CA-26 e a RT-15 da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md); completa FD-03 |
| Requisitos | HA (as escritas continuam), RPO = 0 |
| Suporte na POC hoje | **Parcial.** Incremento A: acima de `RETAIN_DISK_HARD` (fração do volume) ou de `RETAIN_DISK_HARD_BYTES`, o `Compactor` solta só os pinos de reconstrução e, se quem segura o disco é o aplicado local, a célula sai do `/readyz` ("disco no teto duro: reconstrução necessária"). A reconstrução abortada e a nova tentativa são do Incremento B. |
| Injeção | **a** follower com o banco parado (FB-01 variante a) e CRG-2 até o WAL retido dela passar do teto; **b** `RETAIN_DISK_HARD_BYTES` baixo (registrar) com uma reconstrução em curso (Incremento B, FL-03) |
| Duração | até o desfecho + 10 min |

## Análise crítica

- O teto existe para que **uma** célula atrasada não encha o próprio disco até o `ENOSPC` fatal (FD-03). Na variante a, o que segura o disco é o aplicado local (o banco parado): nada pode ser apagado, e a célula se declara fora em vez de cair.
- O teto mede o volume inteiro (`statfs`) além dos bytes do WAL: o arquivo `progress`, o marcador e o que mais estiver no PVC contam.
- Até o Incremento B, a saída da variante a é manual: devolver o banco (a célula alcança pelo disco e o `/readyz` volta) ou reconstruir.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Sem `ENOSPC` | nenhum log `etcdraft: wal.Save` fatal; `kubelet_volume_stats_used_bytes / capacity < 1` |
| A3 | Teto visível | `/status` `wal.tetoDuro == true`; log `disco do log no teto duro` (com taxa limitada) |
| A4 | Variante a: célula fora, sem apagar o que o banco não tem | `wal.seguradoPeloAplicado == true`; `/readyz` 503 com o motivo; `wal.retido` sem cair |
| A5 | Variante a: volta | com o banco de volta, `dr_applied_index` alcança, o disco é liberado e `/readyz` volta a 200 |
| A6 | Variante b (Incremento B) | log `teto duro de disco: reconstrução abortada` na fonte; escritas com ACK; nova tentativa depois de `REBUILD_RETRY` |

## Métricas

- Golden signals: G-ERR-1..5, G-SAT-6 (uso do volume, `VolumeQueueLength`).
- Além: `wal.retido`, `wal.pinos`, `wal.piso` da célula, B-K8S (`kube_pod_status_ready`), RTO-07.

## Lacunas

M-04 (séries Prometheus do bloco `wal`); a reconstrução (Incremento B).

# FD-03 · Disco do WAL cheio

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento: sem compactação (CF-06), o disco **vai** encher |
| Requisitos | FT, HA, RPO = 0 |
| Suporte na POC hoje | **Parcial.** `ENOSPC` no `wal.Save` é fatal. Não há o teto de 70% nem alerta do ADR-041 na POC. Na volta, abrir o WAL e cortar um segmento novo (o WAL do etcd pré-aloca segmentos de 64 MiB) tende a falhar de novo: espera-se reinício em laço até liberar espaço (inferido). |
| Injeção | encher o volume com `fallocate` a partir de um contêiner de depuração com o PVC montado, ou `aws:ssm:send-command` com `AWSFIS-Run-Disk-Fill` apontando para o ponto de montagem; numa follower e, noutra execução, na líder |
| Duração | até a célula voltar após expandir o PVC (`allowVolumeExpansion`) ou apagar o arquivo de enchimento |

## Análise crítica

- O arquivo de progresso do applier mora no mesmo volume: ele também falha com `ENOSPC`. O cenário verifica qual falha chega primeiro e se alguma delas é tratada de outro jeito.
- Num cluster real, as 3 células enchem no mesmo ritmo (mesmo log). Encher uma de propósito é o caso leve.
- A recuperação por expansão online do gp3 tem um tempo próprio (modificação do volume + `resize2fs`/`xfs_growfs` feito pelo driver): medir.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Falha cedo e visível | alerta de disco ≥ 70% disparado **antes** da queda (`kubelet_volume_stats_used_bytes / capacity`); sem alerta configurado: registrar como lacuna |
| A3 | Comportamento na volta | laço de reinício esperado; registrar a mensagem e o número de reinícios |
| A4 | MTTR | T0 → expansão → célula pronta e alcançada |

## Métricas

- Latência (p90, p99, p99.9, max): L-FSYNC antes da queda (sistemas de arquivos quase cheios ficam lentos), L-ACK.
- Golden signals: G-ERR-1..5, G-SAT-6.
- Além: B-K8S (`CrashLoopBackOff`), M-05, RTO-07.

## Lacunas

M-05; teto e alerta do ADR-041.

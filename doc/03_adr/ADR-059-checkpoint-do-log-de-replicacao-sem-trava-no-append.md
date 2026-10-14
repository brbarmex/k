# ADR-059 — Checkpoint do log de replicação sem trava no `Append`, e `Append` fora da goroutine do gate

**Status:** Decidido (Bruno, 14/10/2026: resposta 2a) · **Data:** 14/10/2026 · **Complementa:** [ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)

## Contexto

O [ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md) tirou o fsync do log de replicação do caminho do ACK: cada lote commitado é gravado **sem fsync**, e um checkpoint periódico (`REPLOG_SYNC_INTERVAL`) faz o fsync e publica o `DurableIndex`. No código (`adapter/out/replog` e `usecase/replicate.go`), o fsync voltava ao caminho do ACK por um caminho indireto:

1. O `Sync` do checkpoint segurava a **mesma trava** do `Append` durante todo o fsync.
2. O `CommitPump` chamava o `Append` na **mesma goroutine** que alimenta o gate com o commit do Raft (`SetRaftCommit`) e entrega os lotes ao applier.

Resultado: a cada checkpoint, o próximo lote só chegava ao gate e ao applier depois do fsync terminar. Gate e ACK paravam pelo tempo do fsync, que cresce com o volume acumulado desde o checkpoint anterior (até 1 s de escrita na POC). Na POC sem Azure (gate = commit) isso aparecia como um pico de latência a cada segundo; com a Azure, o progresso dos learners tinha goroutine própria, mas o commit do Raft que o gate também exige ficava parado do mesmo jeito.

Além disso, o `Append` fazia uma chamada de escrita por entrada, e não uma por lote.

## Decisão

1. **O fsync do checkpoint roda fora da trava do `Append`.** Sob a trava, o `Sync` só lê qual arquivo sincronizar e até que índice ele vai; faz o fsync sem a trava; e, de volta sob a trava, publica `DurableIndex = max(atual, índice lido)`. Tudo até o índice lido está nesse arquivo ou em segmentos que já tiveram fsync na rotação, então o `DurableIndex` nunca passa do que está no disco, e a invariante de truncamento do Raft ([ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md), item 3) continua valendo.
2. **Duas travas, com ordem fixa.** `mu` protege o estado e é a única que o `Append` toma; `syncMu` deixa um checkpoint por vez. Quem segura `mu` nunca toma `syncMu`.
3. **Rotação:** o segmento que sai recebe fsync na própria rotação (como antes) e vai para uma lista de aposentados em vez de ser fechado, porque um `Sync` pode estar fazendo fsync nele naquele momento. O `Sync` seguinte fecha os aposentados. A rotação também avança o `DurableIndex` até o fim do segmento que saiu.
4. **`Append` em goroutine própria do `CommitPump`**, ligada à goroutine de commits por uma **fila limitada** (1024 lotes). A goroutine de commits faz só o que é de memória: termo e commit no gate, lote ao applier, lote à fila. Com a fila cheia (disco que não acompanha por muito tempo), ela espera: é a contrapressão, visível e deliberada. Erro de escrita no log de replicação continua fatal ([ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md), item 10).
5. **Uma escrita por lote:** o `Append` monta os registros do lote num buffer e grava uma vez (uma por trecho, quando o lote atravessa a rotação).
6. **Observabilidade:** `dr_replog_sync_duration_seconds` (histograma da duração de cada fsync do checkpoint). Com a correção, um checkpoint lento aparece nesse histograma sem pico correspondente em `dr_write_duration_seconds`.

## Alternativas consideradas

- **Só reduzir `REPLOG_SYNC_INTERVAL`** (o ADR-055 propõe 100 ms ou 8 MiB): o fsync fica menor, mas continua parando o gate a cada checkpoint, agora com mais frequência.
- **`Append` em goroutine própria sem mexer na trava:** o gate deixa de parar, mas o `Append` continua parado durante o fsync, a fila enche mais rápido e a contrapressão chega antes do necessário.
- **Fechar o segmento na rotação esperando o `Sync` em andamento:** o `Append` ficaria parado pelo fsync do checkpoint justamente na rotação. A lista de aposentados evita a espera.

## Consequências

- **O checkpoint não atrasa mais o gate nem o ACK.** O único fsync no caminho do ACK continua sendo o do log do Raft.
- **A rotação ainda espera o disco:** o fsync do segmento que sai roda dentro do `Append`, a cada 64 MiB. Como o checkpoint periódico já sincronizou quase todo o segmento, o que sobra é pouco; pré-alocar segmentos ou rotacionar em segundo plano fica para quando a medição pedir.
- **O log de replicação pode ficar até 1024 lotes atrás do commit.** O `DurableIndex` atrasa na mesma medida, e a compactação do Raft, que só vai até ele, retém um pouco mais em memória. Não muda o RPO: a entrada confirmada está no log do Raft com fsync.
- **Na partida**, o que estava na fila e não foi gravado é refeito a partir do Raft, como qualquer trecho perdido do log de replicação.
- **Um disco do log de replicação lento por muito tempo** enche a fila e volta a segurar a goroutine de commits; se isso durar, o canal de commits do Raft enche e o laço do `Ready` espera (risco 3 da spec da POC). É o comportamento desejado, porque nada pode ser descartado do log de replicação.

## Código

- `DataReplicationApp/internal/adapter/out/replog/replog.go` (`Sync` sem trava, `syncMu`, aposentados, uma escrita por lote), com testes de `Append` durante um fsync preso, rotação com aposentados e `Append` e `Sync` em paralelo.
- `DataReplicationApp/internal/usecase/replicate.go` (`runAppend` com fila limitada) e `checkpoint.go` (duração do `Sync`).
- Spec da POC: AP-01 e AP-10 em [SPEC_POC_LOCAL.md](../../spec/SPEC_POC_LOCAL.md).

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md)
- [D04-dois-logs-e-commitpump.md](../04_diagramas/D04-dois-logs-e-commitpump.md)
- [X04-partida-recuperacao-e-dois-logs.md](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md)

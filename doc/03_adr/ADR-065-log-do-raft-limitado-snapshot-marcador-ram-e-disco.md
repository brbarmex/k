# ADR-065 — Log do Raft limitado: snapshot-marcador, RAM com teto, disco com janela e partida pela cauda

**Status:** Decidido (Bruno, 18/10/2026, rodadas 1, 4, 5 e 6 da compactação) · **Data:** 18/10/2026 · **Registrado como ADR em:** 24/10/2026 · **Substitui:** [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), [ADR-063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md) · **Altera:** [ADR-019](ADR-019-wal-proprio-volume-dedicado.md), [ADR-060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md) · **Afeta o estado atual:** seções 2 (I-08) e 3.5

## Contexto

A POC não compactava o log: o `MemoryStorage` e o WAL cresciam sem limite, a partida relia tudo e as três células chegariam ao OOM quase juntas (~15 GiB/h a 3000 TPS; explicação em `explicacoes/poc_log_sem_compactacao_oom.md`, na pasta compartilhada do projeto). O ADR-063 ficou pendente desde 14/10, e o ADR-041 guardava a retenção da Azure na RAM, com teto só de disco. Em 18/10 o Bruno pediu o redesenho do zero, desconsiderando os ADRs antigos.

## Decisão

1. **Snapshot = marcador**, sem dados: índice, termo, membros (`ConfState`), identidade do grupo (`cluster_id`) e da história (`epoch`) e o índice confirmado no momento do snapshot (que nunca recua). O dado fica no banco da célula. Como o snapshot do WAL do etcd não tem campo de dados, o marcador vai num arquivo `snapshot` próprio, com CRC e fsync (DC-06).
2. **Gatilho:** a cada `SNAPSHOT_EVERY_ENTRIES` (10.000) ou `SNAPSHOT_EVERY_BYTES` (64 MiB), o que vier primeiro. O índice S é o último entregue pelo laço **e** avançado (`Advance`), não o último commit publicado (DC-05).
3. **Ordem fixa,** numa goroutine fora do laço do Raft (`Compactor`): fsync do progresso do applier, arquivo `snapshot`, `wal.SaveSnapshot`, `CreateSnapshot`, compactação da RAM, liberação do disco.
4. **RAM:** compacta até `S − RAFT_CATCHUP_ENTRIES` (5.000); na líder, não abaixo do menor `Match` das followers dentro da janela (DC-08). **Teto duro** `RAFT_RAM_MAX` (256 MiB): passou, compacta até S. `GOMEMLIMIT` em ~80% do limite do container.
5. **Disco:** o WAL do etcd é o único armazenamento. Libera até `min(aplicado local com fsync, piso de retenção, pinos, S)`. O piso é o menor `Match` das réplicas **dentro da janela**, calculado pela líder e publicado às followers junto com o gate. Uma réplica sai da janela quando os bytes retidos desde o seu `Match` passam de `RETAIN_MAX_BYTES` (orçamento de ~1 h de atraso, ~15 GiB a 3000 TPS), e a saída não tem volta (DC-20). **Teto duro** `RETAIN_DISK_HARD` (85% do volume) ou `RETAIN_DISK_HARD_BYTES`: solta os pinos de reconstrução, e as escritas seguem. Se quem segura o disco é o aplicado local, nada dele é apagado e a célula sai do `/readyz`. Sem S3 na v1.
6. **Partida pela cauda:** `wal.Open` a partir do último snapshot; só a cauda volta para a RAM. O applier retoma do progresso P; de P+1 a S, lê do disco pelo `LogReader`. O confirmado do marcador restaura o gate. O metadado do WAL guarda `node_id` e `cluster_id`: outro grupo ou formato antigo não sobem.
7. **Leitor do log:** um só (`LogReader`), fora do laço, da RAM ou dos segmentos `.wal` (`wal.NewDecoder`). Só entrega entrada confirmada por um `HardState` posterior (G3).

## Alternativas consideradas

- **Retenção da Azure na RAM com teto de 70% do disco** (ADR-041): a RAM estourava antes do disco no degradado longo (risco 46, PEND-24).
- **Snapshot com os dados das tabelas:** descartado; o banco da célula já é o estado.
- **`raft.Storage` próprio lendo do disco:** desnecessário com a cauda na RAM e o `LogReader` fora do laço.

## Consequências

- RAM, disco e tempo de partida ficam limitados por configuração, independentes da carga e do atraso de qualquer réplica (R1 a R3, G1, G5, G7).
- Cada snapshot custa ao laço até um fsync extra de espera, porque o `wal` serializa `SaveSnapshot` e `Save`. Medir em `dr_raft_loop_duration_seconds`.
- O padrão de `RETAIN_MAX_BYTES` no código é 2 GiB (POC); o orçamento do produto (~15 GiB) depende do tamanho real da entrada, a medir.
- O estado que hoje só existiria como entrada do log (`SKIP`, janela de dedupe, modo de RPO > 0) ainda não está no marcador. Precisa entrar antes de essas funções existirem.
- Uma réplica que volta pelo canal de recuperação não tem as entradas anteriores ao marcador no próprio WAL e não serve de fonte para elas.

## Na POC (código em 24/10/2026)

**Implementado** (Incremento A, commits `2ba474d` e `38ae207`): `internal/adapter/out/etcdraft/compactor.go`, `retention.go`, `logreader.go`, `marker.go`, `ram.go`; variáveis em `internal/platform/config/config.go`, com as regras de coerência (por exemplo, `RAFT_RAM_MAX > SNAPSHOT_EVERY_BYTES + 32 MiB`, `RETAIN_MAX_BYTES` abaixo do teto duro do volume na partida). O `MsgSnap` recebido só chega ao Raft com o aplicado ≥ índice do marcador (G2).

## Onde está o detalhe

- [especificacao.md](../../spec/compactacao_do_log/especificacao.md), seções 3, 4, 5 e 7
- [decisoes.md](../../spec/compactacao_do_log/decisoes.md)
- [spec_poc_trecho.md](../../spec/compactacao_do_log/spec_poc_trecho.md)

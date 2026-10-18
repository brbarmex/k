# Trecho para a spec da POC: compactação, recuperação e reconstrução

Para incorporar em `spec/SPEC_POC_LOCAL.md` (REASONS Canvas) e implementar via SPDD. Base: main local `c9f42af`. Desenho completo: [especificacao.md](especificacao.md). Decisão de escopo: DO (a POC implementa tudo).

A POC só tem AWS (3 células), então não entram a regra K=1 da Azure, a supressão de candidatura nem a promoção. Também ficam fora deste trecho a troca de membros por ConfChange (D'a) e o RT-10 continua como está. Escolhi esse padrão porque a troca de membros é uma entrega separada e não é necessária para resolver o OOM.

Desvio consciente da D8 na POC: a reconstrução **recria a tabela** da célula, em vez de usar uma tabela sombra. A célula reconstruída fica fora (`/readyz` falso) durante todo o processo, então não há leitura a preservar, e isso evita uma indireção de nome físico no `TableMap`. No produto continua a tabela sombra. Se preferir a sombra já na POC, é só trocar o AP-18.

## A.4 Comportamento do Raft: itens alterados e novos

| ID | Regra |
|---|---|
| RT-09 (alterado) | **Reinício:** `wal.Open` a partir do **último** snapshot gravado; o `ReadAll` só traz para o `MemoryStorage` as entradas depois dele. A retomada do applier continua sendo o progresso (`ResumeFrom`); se ela for anterior ao snapshot, as entradas que faltam vêm do disco pelo `LogReader` (RT-16). Sem progresso válido e com as entradas já apagadas do disco, a célula não sobe e o log pede `task reconstruir CELL=N`. |
| RT-11 (substituído) | **Snapshot-marcador:** uma goroutine `Compactor` por shard, fora do laço do Ready. Gatilho: `SNAPSHOT_EVERY_ENTRIES` (padrão 10.000) ou `SNAPSHOT_EVERY_BYTES` (padrão 64 MiB) desde o último snapshot, o que vier primeiro. Índice S = último commit publicado pelo laço. Ordem: (1) `SaveProgress` com fsync; (2) `wal.SaveSnapshot(S, termo, ConfState)`; (3) `MemoryStorage.CreateSnapshot(S, ConfState, data)`; (4) compactação da RAM (RT-14); (5) liberação do disco (RT-15). `data` é um registro versionado com CRC: `{versao: 1, cluster_id, epoch: 1, confirmado: gate}`. |
| RT-14 (novo) | **RAM:** `MemoryStorage.Compact(S − RAFT_CATCHUP_ENTRIES)` (padrão 5.000), nunca abaixo do primeiro índice atual. Se os bytes no `MemoryStorage` passarem de `RAFT_RAM_MAX` (padrão 256 MiB), compacta até S. O `Compactor` mede os bytes a cada ciclo (`RAFT_COMPACT_INTERVAL`, padrão 1 s). |
| RT-15 (novo) | **Disco:** `limite = min(aplicado local, piso, pinos, S)`. O piso é o menor `Match` das followers dentro da janela, calculado na líder e publicado no `GateUpdate`; a follower usa o último piso recebido. Uma follower sai da janela quando os bytes retidos desde o seu `Match` passam de `RETAIN_MAX_BYTES` (padrão 2 GiB na POC). Liberação: `wal.ReleaseLockTo(limite)` e remoção dos `.wal` sem trava cujo arquivo seguinte começa em índice ≤ limite. Acima de `RETAIN_DISK_HARD` (padrão 85% do volume do Raft), os pinos de reconstrução são soltos. Se o aplicado local for o que segura, nada dele é apagado: no teto duro a célula fica com `/readyz` falso e o `/status` pede reconstrução. |
| RT-16 (novo) | **`LogReader`:** substitui a leitura do `CommittedEntries` pelo `MemoryStorage`. Lê da RAM quando o índice está no `MemoryStorage`; abaixo disso, abre os `.wal` em modo leitura e decodifica com `wal.NewDecoder` (etcd v3.7.2). Aplica a regra da entrada final: buffer de entradas; um índice i que reaparece substitui o buffer a partir de i; só libera as entradas ≤ c ao ler um `HardState` com `commit = c`. Registra um pino no índice que está lendo enquanto lê. Nunca roda no laço do Ready. |
| RT-17 (novo) | **`MsgSnap` (envio):** a líder envia o marcador normalmente e chama `ReportSnapshot(SnapshotFinish)` quando o transporte entrega, ou `SnapshotFailure` se a fila descartar. Substitui o "MsgSnap não enviado" atual em `sendAll`. |
| RT-18 (novo) | **`MsgSnap` (recebimento):** no `Step`, se o aplicado local < índice do marcador, a mensagem é descartada e o `Recovery` é iniciado (AP-17). Com o aplicado ≥ índice, segue para o Raft; no `handleReady`, `rd.Snapshot` não vazio grava `wal.SaveSnapshot`, `MemoryStorage.ApplySnapshot` e move o cursor do `CommitPump` para S + 1. Substitui o erro fatal "snapshot recebido de um par". |
| RT-19 (novo) | **Transporte:** três métodos novos no gRPC entre células. `Retained` devolve o primeiro índice retido em disco e o commit. `CatchUp(from, to)` é um fluxo do servidor com lotes de até 1 MiB de entradas lidas pelo `LogReader`, só até o gate da fonte. `CopyTable(tabela, F)` é um fluxo do servidor com páginas do `Scan` do banco da fonte, com `_dr_v`. |
| RT-20 (novo) | **Identidade:** metadado do WAL = `{node_id, cluster_id}` (JSON), com `CLUSTER_ID` obrigatório e igual nas 3 células. WAL com metadado antigo não sobe: pede `task limpar`. |

## A.5 Comportamento do apply: itens alterados e novos

| ID | Regra |
|---|---|
| AP-01 (alterado) | O `CommitPump` lê as entradas commitadas pelo `LogReader` (RT-16), não direto do `MemoryStorage`. O resto da regra fica igual. |
| AP-06 (alterado) | Progresso com fsync **antes de cada snapshot** (RT-11, passo 1) e antes de cada liberação de disco. Nos demais avanços continua sem fsync. |
| AP-10 (substituído) | A compactação está ligada (RT-11, RT-14, RT-15). |
| AP-15 (novo) | **`GOMEMLIMIT`:** a imagem define `GOMEMLIMIT` por variável, e o compose passa ~80% do `mem_limit` do serviço. O compose passa a ter `mem_limit` por célula (padrão 1 GiB). |
| AP-16 (novo) | **Estados da célula:** `normal`, `recuperando` (AP-17) e `reconstruindo` (AP-18). O `/status` e a métrica `dr_cell_state` mostram o estado; fora de `normal`, `/readyz` é falso e a leitura forte responde 503. |
| AP-17 (novo) | **`Recovery`:** chama `Retained` nos pares e escolhe a fonte com primeiro índice retido ≤ aplicado + 1, preferindo uma follower. Consome o `CatchUp` desde aplicado + 1 e entrega os lotes ao applier pelo mesmo caminho do `CommitPump` (contiguidade, apply condicional, marca d'água, progresso). Termina quando o aplicado alcança o marcador recebido; o próximo `MsgSnap` passa pelo RT-18. Sem nenhuma fonte retendo aplicado + 1, vai para o AP-18. |
| AP-18 (novo) | **Reconstrução:** a fonte fixa F = aplicado no banco dela e registra o pino F − 1 até o fim. Por tabela: a célula apaga e recria a tabela física dela (mesmo `KeySchema` da fonte), consome `CopyTable` e grava cada item com o `_dr_v` original pela condição do AP-04. Depois grava progresso = F com fsync e entra no AP-17 a partir de F + 1. Abortada pelo teto duro (RT-15), recomeça do zero depois de `REBUILD_RETRY` (padrão 1 min). Disparo automático pelo AP-17 ou manual por `POST /admin/reconstruir`. |
| AP-19 (novo) | **Limite da POC:** sem `DeleteItem` (fase 2), a cópia por Scan com versão é suficiente. Quando o DELETE entrar, a reconstrução precisa de lápide ou de cópia consistente (lacuna 6). |

## S.6 Configuração: variáveis novas

| Variável | Padrão | Regra |
|---|---|---|
| `CLUSTER_ID` | (obrigatória) | Igual nas 3 células (RT-20) |
| `SNAPSHOT_EVERY_ENTRIES` | `10000` | De 100 a 10.000.000 |
| `SNAPSHOT_EVERY_BYTES` | `64MiB` | De 1 MiB a 1 GiB |
| `RAFT_CATCHUP_ENTRIES` | `5000` | Menor que `SNAPSHOT_EVERY_ENTRIES` |
| `RAFT_RAM_MAX` | `256MiB` | Maior que `SNAPSHOT_EVERY_BYTES` + 32 MiB (não commitadas) |
| `RAFT_COMPACT_INTERVAL` | `1s` | |
| `RETAIN_MAX_BYTES` | `2GiB` | Menor que o volume do Raft × `RETAIN_DISK_HARD` |
| `RETAIN_DISK_HARD` | `0.85` | Entre 0,5 e 0,95 |
| `REBUILD_RETRY` | `1m` | |
| `GOMEMLIMIT` | ~80% do `mem_limit` | Lida pelo runtime do Go |

Para exercitar os caminhos localmente com pouco tráfego, os testes usam valores baixos, por exemplo `SNAPSHOT_EVERY_ENTRIES=200`, `RAFT_CATCHUP_ENTRIES=50` e `RETAIN_MAX_BYTES=8MiB`.

## S.7 `/status` e métricas novas

`raft.snapshot` (índice), `raft.memoria` (entradas e bytes no `MemoryStorage`), `wal.retido` (bytes e arquivos), `wal.piso`, `wal.pinos`, `celula.estado`, `recuperacao` (fonte, de, até, alcançado) e `reconstrucao` (tabela, itens copiados, F).

Métricas: `dr_raft_mem_bytes`, `dr_raft_mem_entries` (M-04), `dr_raft_snapshot_index`, `dr_raft_snapshot_duration_seconds`, `dr_wal_retained_bytes`, `dr_wal_files`, `dr_retention_floor`, `dr_cell_state`, `dr_catchup_entries_total`, `dr_rebuild_items_total`, `dr_startup_duration_seconds` (M-05).

## S.5 Taskfile: tarefas novas

- `task reconstruir CELL=N`: `POST /admin/reconstruir` na célula N.
- `task test-atrasada CELL=N`: para a célula N, escreve até passar do `RAFT_CATCHUP_ENTRIES`, religa e espera `normal`.
- `task test-fora-da-janela CELL=N`: o mesmo, passando do `RETAIN_MAX_BYTES`.

## SG. Salvaguardas novas

| ID | Regra |
|---|---|
| SG-18 | O disco nunca é liberado além do aplicado local, e o progresso tem fsync antes (RT-15, AP-06). |
| SG-19 | A célula nunca dá `Step` num `MsgSnap` com o aplicado menor que o índice do marcador (RT-18). |
| SG-20 | O `LogReader` só entrega entradas confirmadas como definitivas por um `HardState` posterior (RT-16). |
| SG-21 | O `CatchUp` só envia até o gate da fonte (RT-19). |
| SG-22 | Snapshot, compactação, leitura de `.wal` e reconstrução nunca rodam no laço do Ready. O único efeito aceito no laço é a espera pelo mutex do WAL durante um `SaveSnapshot`. |
| SG-23 | Os bytes no `MemoryStorage` nunca passam de `RAFT_RAM_MAX` por mais de um ciclo do `Compactor`. |

## O.1 Ordem de implementação: passos novos

| ID | Passo | Pronto quando |
|---|---|---|
| O-16 | `LogReader` com a regra da entrada final (RT-16) e `CommitPump` lendo por ele (AP-01) | CA-20 |
| O-17 | `Compactor`: snapshot-marcador, RAM e disco (RT-11, RT-14, RT-15), partida pelo último snapshot (RT-09), identidade (RT-20), `GOMEMLIMIT` e `mem_limit` (AP-15) | CA-21, CA-22, CA-23 |
| O-18 | `MsgSnap` nos dois sentidos (RT-17, RT-18), `Retained` e `CatchUp` (RT-19), `Recovery` (AP-16, AP-17) | CA-24 |
| O-19 | `CopyTable` e reconstrução (AP-18) | CA-25, CA-26 |
| O-20 | Métricas, `/status` e tarefas (S.5, S.7) | CA-27 |

## O.3 Critérios de aceite novos

| ID | Dado / Quando / Então |
|---|---|
| **CA-20** | **Quando** rodo `go test -race ./...`, **então** passa o teste do `LogReader` com um WAL que tem entradas não commitadas sobrescritas por outra líder: só saem as versões commitadas, contíguas, e nada acima do último `HardState.commit`. **E** a leitura que atravessa da RAM para o disco devolve as mesmas entradas que a leitura só do disco. |
| **CA-21** | **Dado** `SNAPSHOT_EVERY_ENTRIES=200`, **quando** escrevo 5.000 entradas, **então** o `/status` das 3 células mostra `raft.snapshot` avançando, `raft.memoria.entradas` ≤ 200 + `RAFT_CATCHUP_ENTRIES` e `wal.retido` caindo depois de cada snapshot. |
| **CA-22** | **Dado** um WAL com 100.000 entradas e snapshot recente, **quando** reinicio uma célula, **então** o `ReadAll` traz só as entradas depois do snapshot (log de partida mostra a contagem) e `dr_startup_duration_seconds` não cresce com o total escrito. |
| **CA-23** | **Dado** uma follower com o banco pausado (`task test-banco-lento`), **quando** a carga continua por mais que `RAFT_RAM_MAX` em bytes, **então** a memória dela fica abaixo do teto (`dr_raft_mem_bytes`), o `CommitPump` passa a ler do disco, e depois de despausar ela alcança o gate sem reiniciar. |
| **CA-24** | **Quando** rodo `task test-atrasada CELL=N`, **então** a célula N passa por `recuperando`, alcança o aplicado da líder, aceita o marcador e volta a `normal`; o `task buscar` mostra os mesmos itens e `_dr_v` nas 3 tabelas. **E** um teste de unidade prova que um `MsgSnap` com índice acima do aplicado não chega ao Raft. |
| **CA-25** | **Quando** rodo `task test-fora-da-janela CELL=N`, **então** a célula N passa por `reconstruindo` e `recuperando`, volta a `normal`, e as 3 tabelas têm os mesmos itens e `_dr_v`, inclusive os itens escritos durante a cópia. |
| **CA-26** | **Dado** `RETAIN_DISK_HARD` baixo, **quando** uma reconstrução passa do teto, **então** ela é abortada, o disco é liberado, as escritas continuam com ACK, e ela recomeça depois de `REBUILD_RETRY`. |
| **CA-27** | **Dado** a carga do CF-06 por 1 h, **então** `process_resident_memory_bytes` e `dr_wal_retained_bytes` ficam estáveis (sem tendência de alta) nas 3 células, e nenhum container é morto por OOM. |

## Riscos da POC (acrescentar à lista)

- **Tamanho da entrega.** Esta é a maior mudança desde o ADR-060 (cinco componentes novos e três métodos gRPC novos), e a apresentação é por volta de 22/10. Se o prazo apertar, a ordem O-16 → O-17 já resolve o OOM e o disco. Os passos O-18 e O-19 são o que torna seguro uma célula ficar atrás.
- Depender do formato interno do WAL do etcd (`wal.NewDecoder`) prende a versão: a atualização do etcd precisa rodar o CA-20 de novo.
- A reconstrução por Scan contra o LocalStack é lenta, porque cada PutItem no LocalStack é lento. Para o CA-25 terminar rápido, a tabela precisa ser pequena.

# Trecho para a spec da POC: compactação, recuperação e reconstrução

Para incorporar em `spec/SPEC_POC_LOCAL.md` (REASONS Canvas) e implementar via SPDD. Base: main local `c9f42af`. Atualizado em 18/10 com a rodada 6 de [decisoes.md](decisoes.md) (DC-01 a DC-12); o canvas é [GGQPA-XXX-202610181712](../../spdd/prompt/GGQPA-XXX-202610181712-[Feat]-service-compactacao-log-raft-recuperacao-reconstrucao.md). Desenho completo: [especificacao.md](especificacao.md). Decisão de escopo: DO (a POC implementa tudo).

A POC só tem AWS (3 células), então não entram a regra K=1 da Azure, a supressão de candidatura nem a promoção. Também ficam fora deste trecho a troca de membros por ConfChange (D'a) e o RT-10 continua como está. Escolhi esse padrão porque a troca de membros é uma entrega separada e não é necessária para resolver o OOM. Backup e restauração do banco e do WAL ficam fora do produto (P-04). Ordem das entregas: A (O-16, O-17) → B (O-18 a O-20) → ConfChange e troca de PVC → multicloud (P-05).

Reconstrução (DC-11, substitui a D8): a cópia **sobrescreve no lugar** a tabela que a célula já tem, pelo `PutItem` condicional por `_dr_v`. Não há tabela sombra nem apagar e recriar, na POC e no produto. A célula fica fora (`/readyz` falso) durante todo o processo.

## A.4 Comportamento do Raft: itens alterados e novos

| ID | Regra |
|---|---|
| RT-09 (alterado) | **Reinício:** `wal.Open` a partir do **último** snapshot gravado; o `ReadAll` só traz para o `MemoryStorage` as entradas depois dele. A retomada do applier continua sendo o progresso (`ResumeFrom`); se ela for anterior ao snapshot, as entradas que faltam vêm do disco pelo `LogReader` (RT-16). Sem progresso válido, ou com o progresso atrás do disco retido, a célula sobe direto em `reconstruindo` (DC-04). Com o volume do Raft vazio, vale o RT-21. |
| RT-11 (substituído) | **Snapshot-marcador:** uma goroutine `Compactor` por shard, fora do laço do Ready. Gatilho: `SNAPSHOT_EVERY_ENTRIES` (padrão 10.000) ou `SNAPSHOT_EVERY_BYTES` (padrão 64 MiB) desde o último snapshot, o que vier primeiro. Índice S = último índice entregue pelo laço **e avançado** (`Advance`), não o commit publicado (DC-05). Ordem: (1) `SaveProgress` com fsync; (2) arquivo `/var/lib/dr/raft/snapshot` com `data` (tmp, fsync, rename; DC-06); (3) `wal.SaveSnapshot(S, termo, ConfState)`; (4) `MemoryStorage.CreateSnapshot(S, ConfState, data)`; (5) compactação da RAM (RT-14); (6) liberação do disco (RT-15). `data` é um registro versionado com CRC: `{versao: 1, cluster_id, epoch: 1, confirmado: gate}`. O registro de snapshot do WAL não guarda `data`; na partida, ele vem do arquivo `snapshot`. |
| RT-14 (novo) | **RAM:** `MemoryStorage.Compact(S − RAFT_CATCHUP_ENTRIES)` (padrão 5.000), nunca abaixo do primeiro índice atual. Na líder, também não compacta abaixo do menor `Match` das followers **dentro da janela** (DC-08; uma follower parada segura a RAM até o teto, sem precisar do canal de recuperação; fora da janela, não segura). Se os bytes no `MemoryStorage` passarem de `RAFT_RAM_MAX` (padrão 256 MiB), compacta até S, ignorando o `Match` das followers. O `Compactor` mede os bytes a cada ciclo (`RAFT_COMPACT_INTERVAL`, padrão 1 s). |
| RT-15 (novo) | **Disco:** `limite = min(aplicado local, piso, pinos, S)`. O piso é o menor `Match` das followers dentro da janela, calculado na líder e publicado no `GateUpdate`; a follower usa o último piso recebido. Uma follower sai da janela quando os bytes retidos desde o seu `Match` passam de `RETAIN_MAX_BYTES` (padrão 2 GiB na POC). Liberação: `wal.ReleaseLockTo(limite)` e remoção dos `.wal` sem trava cujo arquivo seguinte começa em índice ≤ limite. Acima de `RETAIN_DISK_HARD` (padrão 85% do volume do Raft), os pinos de reconstrução são soltos. Se o aplicado local for o que segura, nada dele é apagado: no teto duro a célula fica com `/readyz` falso e o `/status` pede reconstrução. |
| RT-16 (novo) | **`LogReader`:** substitui a leitura do `CommittedEntries` pelo `MemoryStorage`. Lê da RAM quando o índice está no `MemoryStorage`; abaixo disso, abre os `.wal` em modo leitura e decodifica com `wal.NewDecoder` (etcd v3.7.2). Aplica a regra da entrada final: buffer de entradas; um índice i que reaparece substitui o buffer a partir de i; só libera as entradas ≤ c ao ler um `HardState` com `commit = c`. Registra um pino no índice que está lendo enquanto lê. Nunca roda no laço do Ready. |
| RT-17 (novo, Incremento A; DC-01) | **`MsgSnap` (envio):** a líder envia o marcador normalmente e chama `ReportSnapshot(SnapshotFinish)` quando o transporte entrega, ou `SnapshotFailure` se a fila descartar. Substitui o "MsgSnap não enviado" atual em `sendAll`. |
| RT-18 (novo, Incremento A; DC-01) | **`MsgSnap` (recebimento):** no `Step`, um marcador de outro `cluster_id` é descartado. Se o aplicado local < índice do marcador, a mensagem é descartada e o `Recovery` é iniciado (AP-17; no Incremento A, só registra `atrasCauda` no `/status`). Com o aplicado ≥ índice, segue para o Raft; no `handleReady`, `rd.Snapshot` não vazio grava `wal.SaveSnapshot`, `MemoryStorage.ApplySnapshot` e move o cursor do `CommitPump` para S + 1. Substitui o erro fatal "snapshot recebido de um par". |
| RT-19 (novo) | **Transporte:** três métodos novos no gRPC entre células. `Retained` devolve o primeiro índice retido em disco, o commit, o gate, o estado, `cluster_id`, `epoch` e o `Genesis` (DC-11, DC-12); na pré-partida do RT-21, só `{cluster_id, volume vazio, token}`. `CatchUp(from, to)` é um fluxo do servidor com lotes de até 1 MiB de entradas lidas pelo `LogReader`, só até o gate da fonte. `CopyTable(tabela, F)` é um fluxo do servidor com páginas do `Scan` do banco da fonte, com `_dr_v`. A fila de saída por par tem teto em bytes (`PEER_QUEUE_BYTES`, DC-09). |
| RT-20 (novo) | **Identidade:** metadado do WAL = `{node_id, cluster_id, genesis}` (JSON), com `CLUSTER_ID` obrigatório e igual nas 3 células. `genesis` são os tokens de partida vistos no nascimento (RT-21) e não entra na comparação. WAL com metadado antigo não sobe: pede `task limpar`. |
| RT-21 (novo; DC-12) | **Volume do Raft vazio (fail-fast):** antes do `wal.Create`, a célula lê ou grava (fsync) um token de partida em `/var/lib/dr/raft/boot-token`, responde ao `Retained` em modo pré-partida e pergunta aos pares a cada 2 s. Cria o grupo só se todos os pares estão vazios ou se os que rodam têm o token dela no `genesis`, e se a tabela local está vazia. Par sem resposta: espera. Par de outro `cluster_id`, par rodando sem o token dela ou tabela local com itens: **não sobe** (`ErrLostRaftVolume`), e o log aponta para `parklot/adr-troca-de-pvc-em-cell-ativa.md`; na POC, `task limpar`. |

## A.5 Comportamento do apply: itens alterados e novos

| ID | Regra |
|---|---|
| AP-01 (alterado) | O `CommitPump` lê as entradas commitadas pelo `LogReader` (RT-16), não direto do `MemoryStorage`. O resto da regra fica igual. |
| AP-06 (alterado) | Progresso com fsync **antes de cada snapshot** (RT-11, passo 1) e antes de cada liberação de disco. Nos demais avanços continua sem fsync. |
| AP-10 (substituído) | A compactação está ligada (RT-11, RT-14, RT-15). |
| AP-15 (novo) | **`GOMEMLIMIT`:** a imagem define `GOMEMLIMIT` por variável, e o compose passa ~80% do `mem_limit` do serviço. O compose passa a ter `mem_limit` por célula (padrão 1 GiB). O orçamento soma `RAFT_RAM_MAX`, as não commitadas (32 MiB), `PEER_QUEUE_BYTES` × pares, a fila do applier e os fluxos de recuperação (DC-09). |
| AP-16 (novo) | **Estados da célula:** `normal`, `recuperando` (AP-17) e `reconstruindo` (AP-18). O `/status` e a métrica `dr_cell_state` mostram o estado; fora de `normal`, `/readyz` é falso e a leitura forte responde 503. |
| AP-17 (novo) | **`Recovery`:** chama `Retained` nos pares e escolhe a fonte com primeiro índice retido ≤ aplicado + 1, preferindo uma follower. Consome o `CatchUp` desde aplicado + 1, e o próprio `CommitPump`, em modo remoto, entrega os lotes ao applier (único alimentador; contiguidade, apply condicional, marca d'água, progresso; DC-03). Antes de entregar cada lote, a follower registra o gate provado pela fonte: gate = `min(max(commit local, recuperado), recebido)` (DC-02). Termina quando o aplicado alcança o marcador recebido; o próximo `MsgSnap` passa pelo RT-18. Sem nenhuma fonte retendo aplicado + 1, vai para o AP-18. |
| AP-18 (novo) | **Reconstrução:** a célula confere `cluster_id` e `epoch` da fonte contra o seu marcador; diferente, recusa sem gravar e sem nova tentativa automática (DC-11). A fonte fixa F = aplicado no banco dela e registra o pino F − 1 até o `CatchUp` da célula começar, ou até `REBUILD_PIN_TTL` (DC-10). Por tabela: confere que o `KeySchema` da fonte é igual ao da tabela local, consome `CopyTable` e grava cada item **na tabela que já existe** com o `_dr_v` original pela condição do AP-04; nada é apagado nem recriado (DC-11). Depois grava progresso = (F, entrada completa) com fsync e reinicia o processo; na partida, entra no AP-17 a partir de F + 1 (DC-04). Abortada pelo teto duro (RT-15), copia de novo sobre a tabela que ficou depois de `REBUILD_RETRY` (padrão 1 min). Disparo automático pelo AP-17 e pela partida, ou manual por `POST /admin/reconstruir` (202 na follower, 409 na líder). |
| AP-19 (novo) | **Limite da POC:** sem `DeleteItem` (fase 2), a cópia por Scan com versão é suficiente. Com a sobrescrita no lugar (DC-11), um item apagado na fonte continuaria vivo; quando o DELETE entrar, a lápide com `_dr_v` é obrigatória ([parklot/adr-tombstone.md](../../parklot/adr-tombstone.md)). |

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
| `RETAIN_DISK_HARD_BYTES` | `0` (desligado) | Teto duro absoluto; quando definido, maior que `RETAIN_MAX_BYTES` (DC-07) |
| `WAL_SEGMENT_BYTES` | `64000000` | Tamanho do segmento do WAL, de 64 KiB a 1 GiB (DC-07) |
| `REBUILD_RETRY` | `1m` | |
| `REBUILD_PIN_TTL` | `5m` | De 10 s a 1 h (DC-10) |
| `PEER_QUEUE_BYTES` | `64MiB` | De 1 MiB a 1 GiB (DC-09) |
| `GOMEMLIMIT` | ~80% do `mem_limit` | Lida pelo runtime do Go |

Para exercitar os caminhos localmente com pouco tráfego, os testes usam valores baixos, por exemplo `SNAPSHOT_EVERY_ENTRIES=200`, `RAFT_CATCHUP_ENTRIES=50`, `WAL_SEGMENT_BYTES=1MiB`, `RETAIN_MAX_BYTES=4MiB` e `RETAIN_DISK_HARD_BYTES=12MiB`.

## S.7 `/status` e métricas novas

`raft.snapshot` (índice), `raft.memoria` (entradas e bytes no `MemoryStorage`), `wal.retido` (bytes e arquivos), `wal.piso`, `wal.pinos`, `celula.estado`, `recuperacao` (fonte, de, até, alcançado) e `reconstrucao` (tabela, itens copiados, F).

Métricas: `dr_raft_mem_bytes`, `dr_raft_mem_entries` (M-04), `dr_raft_snapshot_index`, `dr_raft_snapshot_duration_seconds`, `dr_wal_retained_bytes`, `dr_wal_files`, `dr_retention_floor`, `dr_cell_state`, `dr_catchup_entries_total`, `dr_rebuild_items_total`, `dr_startup_duration_seconds` (M-05).

## S.5 Taskfile: tarefas novas

Os nomes seguem o inglês do `taskfile.yaml`, com os nomes em português como `aliases`.

- `task rebuild CELL=N` (`reconstruir`): `POST /admin/reconstruir` na célula N.
- `task test-lagging-follower CELL=N` (`test-atrasada`): para a célula N, escreve até passar do `RAFT_CATCHUP_ENTRIES`, reinicia a líder para forçar o `MsgSnap` (DC-08), religa e espera `normal`.
- `task test-out-of-window CELL=N` (`test-fora-da-janela`): o mesmo, passando do `RETAIN_MAX_BYTES`, com escrita lenta durante a cópia.
- `task test-disk-hard-ceiling CELL=N` (`test-teto-disco`): reconstrução abortada pelo teto duro (CA-26).
- `task test-lost-volume CELL=N` (`test-volume-perdido`): apaga o volume do Raft da célula N e confere que ela não sobe e que as escritas continuam (CA-28).
- `task soak DURATION=1h` (`carga-longa`): `load` a 10 TPS e conferência do RSS, do WAL retido e de OOM (CA-27).

## SG. Salvaguardas novas

| ID | Regra |
|---|---|
| SG-18 | O disco nunca é liberado além do aplicado local, e o progresso tem fsync antes (RT-15, AP-06). |
| SG-19 | A célula nunca dá `Step` num `MsgSnap` com o aplicado menor que o índice do marcador (RT-18). |
| SG-20 | O `LogReader` só entrega entradas confirmadas como definitivas por um `HardState` posterior (RT-16). |
| SG-21 | O `CatchUp` só envia até o gate da fonte (RT-19). |
| SG-22 | Snapshot, compactação, leitura de `.wal` e reconstrução nunca rodam no laço do Ready. O único efeito aceito no laço é a espera pelo mutex do WAL durante um `SaveSnapshot`. |
| SG-23 | Os bytes no `MemoryStorage` nunca passam de `RAFT_RAM_MAX` por mais de um ciclo do `Compactor`. |
| SG-24 | A reconstrução nunca apaga nem recria tabela e nunca regride um item; só roda com `cluster_id` e `epoch` da fonte iguais aos locais (AP-18, DC-11). |
| SG-25 | Uma célula com o volume do Raft vazio nunca cria um grupo que já existe; na dúvida, espera (RT-21, DC-12). |

## O.1 Ordem de implementação: passos novos

| ID | Passo | Pronto quando |
|---|---|---|
| O-16 | `LogReader` com a regra da entrada final (RT-16) e `CommitPump` lendo por ele (AP-01) | CA-20 |
| O-17 | `Compactor`: snapshot-marcador, RAM e disco (RT-11, RT-14, RT-15), partida pelo último snapshot (RT-09), identidade (RT-20), `MsgSnap` sem erro fatal (RT-17, RT-18; DC-01), `GOMEMLIMIT` e `mem_limit` (AP-15) | CA-21, CA-22, CA-23 |
| O-18 | `Retained` e `CatchUp` (RT-19), `Recovery` (AP-16, AP-17) | CA-24 |
| O-19 | `CopyTable`, reconstrução por sobrescrita (AP-18) e fail-fast do volume vazio (RT-21) | CA-25, CA-26, CA-28 |
| O-20 | Métricas, `/status` e tarefas (S.5, S.7) | CA-27 |

## O.3 Critérios de aceite novos

| ID | Dado / Quando / Então |
|---|---|
| **CA-20** | **Quando** rodo `go test -race ./...`, **então** passa o teste do `LogReader` com um WAL que tem entradas não commitadas sobrescritas por outra líder: só saem as versões commitadas, contíguas, e nada acima do último `HardState.commit`. **E** a leitura que atravessa da RAM para o disco devolve as mesmas entradas que a leitura só do disco. |
| **CA-21** | **Dado** `SNAPSHOT_EVERY_ENTRIES=200`, **quando** escrevo 5.000 entradas, **então** o `/status` das 3 células mostra `raft.snapshot` avançando, `raft.memoria.entradas` ≤ 200 + `RAFT_CATCHUP_ENTRIES` e `wal.retido` caindo depois de cada snapshot. |
| **CA-22** | **Dado** um WAL com 100.000 entradas e snapshot recente, **quando** reinicio uma célula, **então** o `ReadAll` traz só as entradas depois do snapshot (log de partida mostra a contagem) e `dr_startup_duration_seconds` não cresce com o total escrito. |
| **CA-23** | **Dado** `SNAPSHOT_EVERY_ENTRIES=200` e uma follower com o banco pausado (`task test-slow-db PAUSE=120`), **quando** escrevo 2.000 entradas durante a pausa, **então** ela mantém `dr_raft_mem_entries` ≤ 250, o `CommitPump` passa a ler do disco, e depois de despausar ela alcança o gate sem reiniciar. O teto em bytes (`RAFT_RAM_MAX`) é provado por teste de unidade, porque com TPS baixo ele não é alcançável no notebook. |
| **CA-24** | **Quando** rodo `task test-atrasada CELL=N`, **então** a célula N passa por `recuperando`, alcança o aplicado da líder, aceita o marcador e volta a `normal`; o `task buscar` mostra os mesmos itens e `_dr_v` nas 3 tabelas. **E** um teste de unidade prova que um `MsgSnap` com índice acima do aplicado não chega ao Raft. |
| **CA-25** | **Quando** rodo `task test-fora-da-janela CELL=N`, **então** a célula N passa por `reconstruindo` e `recuperando`, volta a `normal`, e as 3 tabelas têm os mesmos itens e `_dr_v`, inclusive os itens escritos durante a cópia. **E** um teste de unidade prova que a cópia não regride um item mais novo e que uma fonte de outra `epoch` é recusada. |
| **CA-26** | **Dado** `RETAIN_DISK_HARD` baixo, **quando** uma reconstrução passa do teto, **então** ela é abortada, o disco é liberado, as escritas continuam com ACK, e ela recomeça depois de `REBUILD_RETRY`. |
| **CA-27** | **Dado** `task soak DURATION=1h` (o `load` a 10 TPS; os 3.000 TPS do CF-06 são medidos na AWS), **então** `process_resident_memory_bytes` e `dr_wal_retained_bytes` não crescem mais de 10% entre janelas de 10 min nas 3 células, e nenhum container é morto por OOM. |
| **CA-28** | **Dado** o grupo de pé, **quando** rodo `task test-lost-volume CELL=N` com N uma follower, **então** a célula N não sobe, o log cita `troca-de-pvc-em-cell-ativa`, e as escritas pela líder continuam com ACK. **E** depois de `task limpar` e `task subir`, as 3 células sobem juntas, mesmo com uma delas partindo atrasada. |

## Riscos da POC (acrescentar à lista)

- **Tamanho da entrega.** Esta é a maior mudança desde o ADR-060 (cinco componentes novos e três métodos gRPC novos), e a apresentação é por volta de 22/10. Se o prazo apertar, a ordem O-16 → O-17 já resolve o OOM e o disco. Os passos O-18 e O-19 são o que torna seguro uma célula ficar atrás.
- Depender do formato interno do WAL do etcd (`wal.NewDecoder`) prende a versão: a atualização do etcd precisa rodar o CA-20 de novo.
- A reconstrução por Scan contra o LocalStack é lenta, porque cada PutItem no LocalStack é lento. Para o CA-25 terminar rápido, a tabela precisa ser pequena.
- Uma célula que perde o volume do Raft fica fora até `task limpar` (RT-21); com ela fora, o grupo não tolera mais nenhuma falha. A devolução segura é a feature `troca-de-pvc-em-cell-ativa`.

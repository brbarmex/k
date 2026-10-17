# Tabela de structs

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice da implementação](README.md)

Nomes, pacote e o que cada struct (ou interface) faz. Os nomes de pacote seguem a [árvore de pastas](01_principios_bibliotecas_e_pastas.md#3-estrutura-de-pastas).

## Tabela de structs

| Struct | Pacote | O que faz |
|---|---|---|
| `Entry` | `domain` | Unidade replicada e versionada (índice = versão do item). Um `BatchWriteItem` vira uma `Entry` |
| `Op` | `domain` | Uma mutação: tabela lógica, chave e imagem final (`Put` ou `Delete`) |
| `Gate` | `domain` | Calcula `min(commit Raft, 2º maior índice dos learners)`, nunca recua, acorda quem espera um índice. Guarda o **termo** atual e só adota progresso desse termo (`SetAzureDurable(termo, índice, learner)`, [ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)); `WaitFor` remove o waiter ao expirar; `Ready()` diz se foi reconstruído no termo novo |
| `Lease` | `domain` | Validade D do modo degradado, contada no relógio monotônico da líder desde `t_c` (último contato com a maioria Azure, alimentado por `ObserveContact`), não desde a proposta do `DEGRADE`, que só marca o modo (`Mark`). `Check` a cada ACK confere `now < t_c + D`; salto de relógio conta como vencido; `Inherit` (líder nova, reinício) deixa o lease vencido até contato ou `RENEW`; `Renew` só humano ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)) |
| `Clock` | `domain` | Função de relógio injetável (`time.Now` em produção, relógio falso nos testes) |
| `DegradedPolicy` | `domain` | `accept` ou `reject`, obrigatória na configuração ([ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)) |
| `ClusterMode` | `domain` | `normal` ou `degraded`, mais o `epoch` do último DEGRADE/RESTORE/RENEW |
| `TableMap` | `domain` | Converte nome lógico (`{cell}`) em físico e valida a lista permitida |
| `WriteRequest` / `WriteResult` | `port` | Entrada e saída da porta `Writer`; o pedido carrega `Forwarded` (nunca reencaminhar, [ADR-039](../03_adr/ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md)); o resultado carrega a flag `Degraded` (header D10), vinda do estado real do shard |
| `LeaderInfo` | `port` | Quem é a líder e se é o nó local |
| `LearnerProgress` | `port` | `(termo, índice, identidade, SentAt)` do learner, devolvido na resposta do `AppendEntries` ou do heartbeat; a única fonte de progresso da Azure e de `t_c` ([ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)) |
| `AppliedWaiter` | `port` | Interface: bloqueia até o `Applier` aplicar um índice; o ACK depende dela |
| `VersionedOp` | `port` | `Op` + versão, o que o `StateStore` aplica de forma condicional |
| `WriteService` | `usecase` | Fluxo de escrita: acha a líder (espera até `WAIT_LEADER` e responde 503, sem `Propose`), encaminha ou propõe, espera o gate (com `DEGRADE_AFTER` e a política), espera o apply local, **confere o lease a cada ACK** e responde |
| `DegradeService` | `usecase` | Dono do estado degradado do shard: política, `Enter`/`Restore`/`Renew` por entradas replicadas, `Adopt` (herda o `DEGRADE` sem `t_c`), `Active` (alimenta `x-dr-mode`), `CheckLease`. Com `reject` nunca propõe `DEGRADE` |
| `BatchService` | `usecase` | Valida o lote (até 25 itens), gera uma `Entry`, **dedupe por requisição inteira** (id estável do SDK, [ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md)), não por item; o resultado por item vem do applier ([ADR-048](../03_adr/ADR-048-skip-por-item.md)). Desenho do apply em [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md): `BatchWriteItem` não aceita condição |
| `ReadService` | `usecase` | Leitura forte (barreira `min(ReadIndex, gate)`, `appliedIndex` alcança a barreira, [ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)) só na tabela base; Query em GSI recusada ([ADR-050](../03_adr/ADR-050-query-em-gsi-recusada.md)) |
| `Applier` | `usecase` | Consome o log até o `gate` e aplica no banco **sempre depois do gate** ([ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md)), **em paralelo por chave**: `APPLY_WORKERS` workers, a mesma chave sempre no mesmo worker e na ordem do log ([ADR-058](../03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)). O `applied` é a marca d'água contígua. Persiste o **checkpoint por item** `(índice, ordem)` da marca d'água no volume do log; o `SKIP` identifica um item ([ADR-048](../03_adr/ADR-048-skip-por-item.md)); a consulta devolve `APPLIED`, `REJECTED` ou `PENDING` por item |
| `Rebuilder` | `usecase` | Reconstrução em tabela sombra: índice F, cópia difusa condicional, replay do log desde F, cutover do nome lógico com geração, checksum por faixa ([ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)) |
| `DegradeController` | `usecase` | Na versão final, o monitor que decide DEGRADE e RESTORE com histerese e chama o `DegradeService` (no esboço, quem espera a Azure por `DEGRADE_AFTER` dispara o `Enter`) |
| `LeadershipService` | `usecase` | Transferência planejada de liderança e detecção de disco lento |
| `Recovery` | `usecase` | Na partida: completa o log de replicação a partir do Raft e reabre leitura quando o applier alcança o commit do `HardState` (proposta do [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md)) |
| `DedupeIndex` | `usecase` | Janela de dedupe por id do SDK (hash como fallback), mantida por todos os votantes a partir do log ([R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md)) |
| `Server` | `adapter/in/dynamohttp` | Servidor fasthttp, tradução de protocolo e de erros do DynamoDB (JSON por `json.Marshal`, `APIError` tipado, `WriteTimeout` = `REQUEST_DEADLINE` + folga, `IdleTimeout` da configuração, `x-dr-mode`, `x-dr-code`). Recusa Query e Scan em GSI. Aplica a lista identidade -> tabelas do mTLS ([ADR-052](../03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md)); `_dr_v` nunca sai nas respostas ([ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)) |
| `PeerServer` | `adapter/in/peergrpc` | Recebe replicação e forward de outras células |
| `AdminServer` | `adapter/in/adminhttp` | `/livez`, `/readyz`, `/metrics` |
| `Log` | `adapter/out/replog` | Implementa `ReplicationLog` e `ApplyProgress` (log de replicação, vida longa): segmentos com CRC32C, gravação sem fsync, `Sync` que faz o fsync fora da trava do `Append` e publica o `DurableIndex` ([ADR-059](../03_adr/ADR-059-checkpoint-do-log-de-replicacao-sem-trava-no-append.md)), `CompactPrefix` por segmento inteiro, leitura por faixa para Azure e `drctl`, cauda rasgada truncada na abertura ([ADR-055](../03_adr/ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)) |
| `Applier` | `usecase` | Aplica até o gate, em paralelo por chave, item a item com versão; grava o progresso `(índice, ordem)` da marca d'água; implementa `AppliedWaiter` ([ADR-058](../03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)) |
| `Checkpointer` | `usecase` | A cada intervalo: `Sync` do log de replicação, compacta o Raft até `RaftCompactLimit` e trunca o log até `ReplogTruncateLimit` |
| `RaftNode` | `adapter/out/etcdraft` | Implementa `ConsensusEngine` sobre `etcd raft`; mantém os learners |
| `RaftStorage` | `adapter/out/etcdraft` | Log do Raft: `wal` do etcd por shard mais `raft.MemoryStorage`, fsync antes do ACK; compacta só até o `DurableIndex` do `replog.Log`, com folga `RAFT_RETAIN_MAX` |
| `CanSendBeforeSync` (função) | `domain` | Regra do modo síncrono com F3: só `MsgApp` da líder sai antes do fsync; `SplitBeforeSync` separa as mensagens do `Ready` |
| `CommitPump` | `usecase` | Duas goroutines: **progresso** (cada resposta de learner alimenta o gate e o `t_c` do lease na hora) e **commits** (distribui os lotes a gate, applier e log de replicação). Ignora lote vazio e devolve `ErrStreamClosed` com canal fechado |
| `DynamoStore` | `adapter/out/dynamodb` | Implementa `StateStore` com escrita condicional por `_dr_v`; cria a tabela sombra e executa o cutover por geração |
| `CosmosStore` | `adapter/out/cosmos` | Implementa `StateStore` pelo Cloud-Adapter, tratando RU e 429 |
| `PeerClient` | `adapter/out/peerclient` | Streams gRPC por par de nós, canais separados para replicação e forward |
| `Resolver` | `adapter/out/discovery` | Resolve o DNS estável de cada célula |
| `Config` | `platform/config` | Configuração imutável validada na partida; `DEGRADED_POLICY` obrigatória e sem padrão, `DEGRADED_LEASE` sem padrão e obrigatória só com `accept` (ignorada com `reject`), `READ_MODE_DEFAULT`, `WRITE_MODE` e `MAX_STALENESS` recusadas (modo único strong, ADR-057), prazos em ordem (`WAIT_LEADER` <= `FORWARD_TIMEOUT` < `REQUEST_DEADLINE` < `CLIENT_TIMEOUT`) |
| `App` | `bootstrap` | Grafo do Fx e hooks de ciclo de vida |

Structs que **já existem** no esboço compilado: `Entry`, `Op`, `Gate`, `Lease`, `LearnerProgress`, `WriteService`, `DegradeService`, `CommitPump`, `Server`, `Config` e `App` (montagem). Os demais são desenho.

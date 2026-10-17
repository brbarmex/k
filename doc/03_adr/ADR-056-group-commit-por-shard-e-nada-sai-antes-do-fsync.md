# ADR-056 — Group commit por shard, nada sai antes do fsync e `AsyncStorageWrites` decidido por spike

**Status:** Decidido (Bruno, 13/10/2026: respostas 1a 2a 3b 4a 5a 6a) · **Data:** 13/10/2026 · **Complementa:** [ADR-019](ADR-019-wal-proprio-volume-dedicado.md), [ADR-030](ADR-030-verificacao-formal-tla.md), [ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)

## Contexto

O Bruno pediu (1) fsync com group commit e buffer em memória enquanto as escritas acontecem e (2) a avaliação do `AsyncStorageWrites` do `etcd-io/raft` para batching adaptativo e pipelining de persistência. A análise completa, conferida no código do `go.etcd.io/raft/v3` v3.6.0 e do `wal` do `go.etcd.io/etcd/server/v3` v3.6.5, está em [analise_group_commit_e_async_storage_13_10](../06_pendencias_e_riscos/analise_group_commit_e_async_storage_13_10.md). Os pontos que decidem:

- **O group commit por shard já existe.** As propostas que chegam enquanto o fsync anterior roda vão juntas no próximo `Ready`, e o `wal.Save` grava o lote com **um** `fdatasync` (segmentos de 64 MB pré-alocados, escrita alinhada a 4 KiB). Com pouco tráfego, o lote tem uma entrada; em rajada, cresce sozinho.
- **Entre shards não existe**, porque há um WAL por shard. O custo disso é **IOPS**, não latência: com 8 shards a 3.000 TPS, ~1.900 a 3.800 IOPS de escrita por nó, contra 3.000 de base no gp3 e no Premium SSD v2.
- **`AsyncStorageWrites` é seguro por construção** (votos, `MsgAppResp` e o auto-ACK da líder viajam como respostas da escrita e só saem depois do fsync), mas o ganho de latência é o mesmo da líder enviar `MsgApp` antes de gravar no modo síncrono (F3). O que só o assíncrono dá: o laço do Raft não para num fsync lento. O etcd server não usa esse modo; o CockroachDB usa.

## Decisão

1. **Nada sai antes do fsync (1a).** O buffer em memória é **só a área de montagem do lote**. Nenhum ACK ao cliente, nenhum voto (`MsgVoteResp`) e nenhuma resposta de `AppendEntries` (`MsgAppResp`) sai antes do fsync que cobre a entrada, em nenhum dos 6 nós. A líder só se conta no quórum depois do próprio fsync. É uma **invariante** (vale no ADR, no TLA+ e nos testes), não uma configuração. A decisão de 12/10 (sem ACK em memória) continua.
2. **Group commit por shard (2a).** Um `wal` do etcd por shard, como no ADR-019. O laço do `Ready` de cada shard grava **tudo o que está pendente** num único `Save` com um `fdatasync` e só então libera as respostas, em ordem. Nada de WAL compartilhado na v1.
   - **IOPS provisionadas** no volume do Raft (io2 ou gp3 com IOPS acima da base na AWS; Premium SSD v2 com IOPS acima da base na Azure) e **teto de shards por nó** (`MAX_SHARDS_PER_NODE`, valor do S1).
   - **Gatilho do plano B** (WAL compartilhado entre shards, o W2): o S1 mede, na carga de 2x (6.000 TPS), fsync P99 acima do orçamento da seção 7 **ou** IOPS de escrita acima de 70% do provisionado. Se o gatilho disparar, a volta do W2 é uma decisão nova, com a bateria completa de testes de armazenamento.
3. **Modo síncrono com F3 na fase 1; `AsyncStorageWrites` decidido no spike S1b (3b).**
   - Fase 1: `RawNode` síncrono. **Só a líder** pode enviar antes de gravar, e **só `MsgApp`**. Qualquer outra mensagem do `Ready` (em especial `MsgAppResp` e `MsgVoteResp`) sai depois do `Save` com fsync. `Advance` só depois do fsync.
   - Trocar de modo fica restrito ao adaptador `etcdraft`.
   - **Critério do S1b:** o assíncrono entra se reduzir o **P99 do commit em pelo menos 20%** **ou** eliminar eleições causadas por fsync lento (injeção de atraso de disco de 50 ms a 500 ms na líder). Sem isso, fica o síncrono.
   - Não haverá variável para escolher o modo em produção (a matriz de testes dobraria).
4. **Sem espera fixa para formar lote (4a).** O lote é o que se acumulou enquanto o fsync anterior rodava, com **teto de 1 MiB** (`RAFT_BATCH_MAX_BYTES`); uma entrada maior que o teto vai sozinha. Espera adaptativa só volta à mesa se o S1 mostrar IOPS no limite.
5. **Limite do buffer e contrapressão (5a).** `MaxUncommittedEntriesSize` = **32 MiB por shard** (`RAFT_MAX_UNCOMMITTED_BYTES`) e `MaxInflightBytes` configurado, com a soma dos shards dentro da meta de 256 MiB do pod. Acima do limite, a proposta é recusada com **`ThrottlingException`** (HTTP 400, que os SDKs repetem com backoff); o dedupe de 5 min ([ADR-026](ADR-026-dedupe-de-retry-id-estavel.md)) cobre a repetição.
6. **TLA+ com log volátil e durável (6a).** O modelo do [ADR-030](ADR-030-verificacao-formal-tla.md) separa log volátil e log durável por nó, tem queda que perde o volátil em qualquer passo e só deixa `MsgAppResp`, voto e auto-ACK da líder dependerem do durável. Invariante nova: **toda entrada ACKada está no log durável de pelo menos 2 nós AWS e 2 nós Azure**. O modelo cobre o modo síncrono com F3 e o assíncrono, antes do código do adaptador.

## Regras de implementação

- **Ordem no laço do `Ready`:** `Save` (entradas e `HardState`) → `fdatasync` → `MemoryStorage.Append` → entregar respostas. Inverter quebra o envio de `MsgApp` a seguidores atrasados.
- **Junção de lotes:** uma escrita posterior pode reescrever entradas da anterior (troca de termo). A junção corta o lote anterior a partir do primeiro índice da nova, usa o último `HardState` e trata snapshot como fronteira de lote.
- **Falha de escrita ou de fsync é fatal** ([ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)): o processo encerra **sem entregar nenhuma resposta do lote**.
- **Gate:** o progresso dos learners continua vindo só do `MsgAppResp` no termo atual ([ADR-046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)); como a resposta só sai depois do fsync do learner, o gate não conta durabilidade inexistente.
- **Applier:** só recebe entrada estável no disco da própria líder (no assíncrono a biblioteca já garante; no síncrono, pela ordem `Save` → `Advance`).

## Alternativas consideradas

- **Responder antes do fsync com janela de exposição limitada (1b, M3 da seção 6.8):** reverteria a decisão de 12/10 e exigiria aceite de RPO > 0 em falha correlacionada. Descartado.
- **WAL compartilhado entre shards já na v1 (2b):** um fsync para todos os shards, mas código próprio de armazenamento de volta ao caminho do ACK, que era o maior risco de perda de dado. Fica como plano B com gatilho.
- **`AsyncStorageWrites` já na fase 1 (3a):** mais robusto contra fsync lento, mas caminho menos usado (o etcd server não usa) e contrato mais complexo, sem número que mostre ganho além do F3.
- **Os dois modos atrás de variável (3c):** dobra a matriz de testes.
- **Espera adaptativa (4b):** só se o S1 mostrar IOPS no limite.
- **Só os limites padrão da biblioteca (5b):** deixa o buffer crescer até estourar a memória do pod antes de qualquer alarme.
- **Confiar só no TLA+ do `etcd-io/raft` (6b):** ele não cobre o nosso gate nem o lease.

## Consequências

- **Latência:** com F3, o tempo até o gate passa de `t_L + max(RTT_A + t_S, RTT_Z + t_Z)` para `max(t_L, RTT_A + t_S, RTT_Z + t_Z)`. O apply ainda espera o disco da líder; F7 (transferir a liderança com fsync lento) continua necessário.
- **Risco novo do modo síncrono:** a regra "o que pode sair antes do disco" fica no nosso adaptador. Enviar todas as mensagens antes do `Save` faz um learner confirmar antes do próprio fsync e o gate contar uma durabilidade que não existe. Teste obrigatório: nenhuma mensagem além de `MsgApp` da líder sai antes do fsync (risco 34).
- **Fsync lento no modo síncrono congela o laço do Raft:** sem heartbeats, seguidores podem iniciar eleição e o lease do modo degradado pode parecer vencido. Atenuação: `ElectionTick` bem acima do P99.9 de fsync e F7. É o principal argumento do S1b (risco 35). **Atualizado pelo [ADR-060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md):** o `ElectionTick` virou configuração (`ELECTION_TICKS`, 30 na POC) e o laço não espera mais o apply; o fsync continua dentro do laço, e esse é o resto do risco 35.
- **IOPS:** custo de volume provisionado nos 6 nós, por cliente ([R20](../02_cenarios/a_rever/R20-custo-6x.md)); risco 36.
- **Memória:** até 32 MiB por shard não persistidos; métrica e alerta.
- **Métricas novas:** `raft_unpersisted_bytes`, `raft_batch_entries`, `raft_batch_bytes`, `raft_fsync_per_second`, `raft_fsync_seconds` (P50, P99, P99.9), `raft_throttled_total`, `raft_elections_total`.
- **Spikes:** S1 mede IOPS por número de shards na carga de 2x; S1b novo compara síncrono e assíncrono.
- **Pendência:** PEND-23 (valores: teto de shards por nó, IOPS provisionadas, `ElectionTick`, `MaxInflightBytes`).

## Código de exemplo

- `internal/domain/sendrule.go`: `CanSendBeforeSync`, a regra do que pode sair antes do fsync no modo síncrono, com testes.

## Atualização de 14/10/2026 (inventário dos ADRs, resposta 6b)

**Na POC o lote do log do Raft vem só do `Ready`:** cada `Ready` é gravado com um `wal.Save` e um `fdatasync`, o que já junta as propostas que chegaram durante o fsync anterior. O teto de 1 MiB por lote **entra junto com o `AsyncStorageWrites`**, se o spike S1b o aprovar. A regra "nada sai antes do fsync" continua garantida pela ordem do laço síncrono: `MsgApp` da líder, `Save` com fsync, e só então o resto das mensagens (`domain.SplitBeforeSync`).

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md) (6.8 e 6.10)
- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.5.2)
- [D04-commitpump-e-log-do-raft.md](../04_diagramas/D04-commitpump-e-log-do-raft.md), [D05-gate-e-quorum.md](../04_diagramas/D05-gate-e-quorum.md)
- [06_testes_e_spikes.md](../05_implementacao/06_testes_e_spikes.md) (S1 e S1b)

# Tabela de structs

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice da implementação](README.md)

Nomes, pacote e o que cada struct (ou interface) faz. Os nomes de pacote seguem a [árvore de pastas](01_principios_bibliotecas_e_pastas.md#3-estrutura-de-pastas).

## Tabela de structs

| Struct | Pacote | O que faz |
|---|---|---|
| `Entry` | `domain` | Unidade replicada e versionada (índice = versão do item). Um `BatchWriteItem` vira uma `Entry` |
| `Op` | `domain` | Uma mutação: tabela lógica, chave e imagem final (`Put` ou `Delete`) |
| `Gate` | `domain` | Calcula `min(commit Raft, maioria Azure)`, nunca recua, acorda quem espera um índice |
| `WriteMode` / `ReadMode` | `domain` | Enumerações `strong`/`eventual` lidas da configuração |
| `ClusterMode` | `domain` | `normal` ou `degraded`, mais o `epoch` do último DEGRADE/RESTORE |
| `TableMap` | `domain` | Converte nome lógico (`{cell}`) em físico e valida a lista permitida |
| `WriteRequest` / `WriteResult` | `port` | Entrada e saída da porta `Writer`; o resultado carrega a flag `Degraded` (header D10) |
| `LeaderInfo` | `port` | Quem é a líder e se é o nó local |
| `AppliedWaiter` | `port` | Interface: bloqueia até o `Applier` aplicar um índice; o ACK depende dela |
| `VersionedOp` | `port` | `Op` + versão, o que o `StateStore` aplica de forma condicional |
| `WriteService` | `usecase` | Fluxo de escrita: encaminha ou propõe, espera o gate **e o apply local**, responde |
| `BatchService` | `usecase` | Valida o lote (até 25 itens), gera uma `Entry`, dedupe por item. Desenho do apply em [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md): `BatchWriteItem` não aceita condição |
| `ReadService` | `usecase` | Leitura strong (`ReadIndex` + espera de `appliedIndex`) e eventual |
| `Applier` | `usecase` | Consome o log até o `gate`, preserva ordem por chave, aplica no banco |
| `DegradeController` | `usecase` | Decide DEGRADE e RESTORE com histerese e propõe a entrada |
| `LeadershipService` | `usecase` | Transferência planejada de liderança e detecção de disco lento |
| `Recovery` | `usecase` | Na partida: completa o log de replicação a partir do Raft e reabre leitura no high-water mark |
| `DedupeIndex` | `usecase` | Janela de dedupe por id do SDK (hash como fallback), mantida por todos os votantes a partir do log ([R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md)) |
| `Server` | `adapter/in/dynamohttp` | Servidor fasthttp, tradução de protocolo e de erros do DynamoDB |
| `PeerServer` | `adapter/in/peergrpc` | Recebe replicação e forward de outras células |
| `AdminServer` | `adapter/in/adminhttp` | `/livez`, `/readyz`, `/metrics` |
| `WAL` | `adapter/out/wal` | Implementa `ReplicationLog` (log de replicação, vida longa): lote, retenção, leitura para Azure e `drctl` |
| `Segment` | `adapter/out/wal` | Um arquivo pré-alocado de 64 MiB com índice esparso |
| `RaftNode` | `adapter/out/etcdraft` | Implementa `ConsensusEngine` sobre `etcd raft`; mantém os learners |
| `RaftStorage` | `adapter/out/etcdraft` | Log do Raft: armazenamento que a lib exige, em arquivo próprio (`raftlog`), com fsync antes do ACK |
| `CommitPump` | `usecase` | Recebe os lotes commitados e os distribui: gate, applier (memória) e log de replicação (lote assíncrono) |
| `SegFile` | `platform/segfile` | Motor de arquivo segmentado compartilhado pelos dois logs, sem regra de negócio |
| `DynamoStore` | `adapter/out/dynamodb` | Implementa `StateStore` com escrita condicional por `_dr_v` |
| `CosmosStore` | `adapter/out/cosmos` | Implementa `StateStore` pelo Cloud-Adapter, tratando RU e 429 |
| `PeerClient` | `adapter/out/peerclient` | Streams gRPC por par de nós, canais separados para replicação e forward |
| `Resolver` | `adapter/out/discovery` | Resolve o DNS estável de cada célula |
| `Config` | `platform/config` | Configuração imutável validada na partida |
| `App` | `bootstrap` | Grafo do Fx e hooks de ciclo de vida |

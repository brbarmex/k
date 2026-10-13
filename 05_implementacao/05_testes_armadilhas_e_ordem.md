# Testes por camada, armadilhas e ordem de implementação

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice da implementação](README.md)

O plano de **spikes e testes de sistema** está em [06_testes_e_spikes.md](06_testes_e_spikes.md).

## Estratégia de testes por camada

| Camada | Como testa | Detalhe |
|---|---|---|
| `domain` | Unitário puro e *property-based* | `Gate` nunca recua, para qualquer ordem de eventos |
| `usecase` | Fakes das portas, relógio injetado | Falhas determinísticas: líder cai entre `Propose` e gate |
| `port` | **Suíte de conformidade** | A mesma bateria roda no WAL real e no fake; idem Raft e `StateStore` |
| `adapter` | Integração com contêiner (DynamoDB Local, Azurite/emulador do Cosmos) | Compatibilidade com SDK Java, Go e .NET |
| Sistema | `test/fault` + Jepsen-like | Disco lento, partição AWS↔Azure, kill -9 no meio do fsync |
| Protocolo | `test/tla` | Gate, DEGRADE/RESTORE e troca de líder (D12), **antes** do código do gate |

## Armadilhas que o desenho já evita

1. **Corpo reutilizado do fasthttp:** o parser copia para o buffer da `Entry` antes de retornar do handler.
2. **Fx escondendo ciclo de dependência:** `ValidateApp` no CI.
3. **Interface grande demais:** cada porta tem menos de dez métodos; se crescer, é sinal de que mistura duas responsabilidades.
4. **Vazamento do Raft para o núcleo:** `depguard` quebra o build.
5. **Log no caminho quente:** só métricas; um teste de benchmark falha se houver alocação no `Write`.
6. **Contexto sem prazo:** toda chamada de porta recebe `context` com deadline herdado da requisição, sempre menor que o timeout de 30 s do SDK ([9.4](../01_arquitetura/08_resiliencia_autocura_e_timeouts.md)).

## Ordem sugerida de implementação

1. `domain` + `Gate` + testes (já esboçado) e o modelo TLA+.
2. Portas + fakes + `WriteService` (escrita ponta a ponta em memória).
3. Adaptador `wal` + suíte de conformidade + `fio` (spike S1).
4. Adaptador `etcdraft` sobre o WAL + conformidade do `ConsensusEngine`.
5. `dynamohttp` + `Applier` + `DynamoStore` (um nó, depois três).
6. `peergrpc` + learners da Azure + gate real.
7. `DegradeController`, `Recovery`, `LeadershipService`.
8. `CosmosStore`, `drctl`, observabilidade e testes de falha.

## Decisões de biblioteca (Bruno, 12/10)

1. **HTTP: decidido `fasthttp`** atrás da porta, com benchmark contra `net/http` no S1 como verificação.
2. **Logger: decidido `zerolog`.**
3. **Config e env: decidido `viper`** para YAML e env; o peso é medido nos spikes, com `koanf` como plano B.

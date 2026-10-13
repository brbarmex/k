# Testes por camada, armadilhas e ordem de implementação

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice da implementação](README.md)

O plano de **spikes e testes de sistema** está em [06_testes_e_spikes.md](06_testes_e_spikes.md); as **cinco fases** em que o trabalho se divide estão em [07_fases_de_trabalho.md](07_fases_de_trabalho.md).

## Estratégia de testes por camada

| Camada | Como testa | Detalhe |
|---|---|---|
| `domain` | Unitário puro, *property-based* e de **concorrência com `-race`** | `Gate` nunca recua, para qualquer ordem de eventos e vindo de vários learners ao mesmo tempo; só adota o termo atual; `WaitFor` não deixa waiter para trás; `Lease` vence em `t_c + D` (último contato com a maioria Azure, não a proposta do `DEGRADE`), nasce vencido numa líder nova e trata salto de relógio como vencido |
| `usecase` | Fakes das portas, relógio injetado, `-race` | Falhas determinísticas: líder cai entre `Propose` e gate; sem líder espera `WAIT_LEADER` e não propõe; lease conferido a cada ACK (pausa de processo); `reject` nunca propõe `DEGRADE`; gate alimentado por evento com `Append` lento; lote vazio e canal fechado |
| `port` | **Suíte de conformidade** | A mesma bateria roda no WAL real e no fake; idem Raft e `StateStore` |
| `adapter` | Integração com contêiner (DynamoDB Local, Azurite/emulador do Cosmos) | Compatibilidade com SDK Java, Go e .NET; erros JSON válidos; `WriteTimeout` coerente com o prazo; `ConsistentRead` em GSI recusada; mTLS com a lista identidade -> tabelas |
| Sistema | `test/fault` + Jepsen-like | Disco lento, partição AWS↔Azure, kill -9 no meio do fsync, relógio com salto |
| Protocolo | `test/tla` | Gate por termo, lease, promoção, troca de membros, dedupe, DELETE e replay (D12, [ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)), **antes** do código do gate |

### Testes novos da revisão crítica de 13/10/2026

| Teste | Onde | O que prova |
|---|---|---|
| Lease | `domain` e `usecase` | Validade D desde `t_c` (último contato com a maioria Azure); proposta tardia com `t_c` antigo nasce vencida; líder nova ou nó reiniciado sem `t_c` fica vencido até contato ou `RENEW`; `RENEW` libera; conferência a cada ACK; `503 LEASE_EXPIRED`; `DEGRADED_POLICY=reject` nunca degrada ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)) |
| Gate por evento | `usecase` | Cada resposta de learner libera o gate sem commit novo, mesmo com `Append` bloqueado ou applier parado ([ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)) |
| `I-DEL` por propriedade | `test/` | Sequências aleatórias de Put e Delete com queda em qualquer ponto do replay convergem para o mesmo estado; leituras só abrem depois do high-water mark ([ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)) |
| Conformidade DynamoDB x camada x Cosmos | `test/conformance` | A mesma sequência (erros, paginação, tamanhos, tipos numéricos, TTL, `_dr_v` ausente) dá o mesmo resultado nos três; o Cosmos não "melhora" o GSI ([ADR-050](../03_adr/ADR-050-gsi-nao-tem-leitura-forte.md), [ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)) |
| mTLS | `adapter` e sistema | Sem certificado a conexão cai; identidade sem entrada na lista recebe `403`; identidade fora da operação ou da tabela também ([ADR-052](../03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md)) |
| SKIP por item | `usecase` e sistema | O `SKIP` de `(índice, ordem)` pula só o item; os demais itens do lote são aplicados; o replay depois de queda reaplica os anteriores e pula o marcado; a consulta devolve `APPLIED`, `REJECTED`, `PENDING` por item ([ADR-048](../03_adr/ADR-048-skip-por-item.md)) |
| Leitura forte | `usecase` | Barreira `min(ReadIndex, gate)`; líder recém-eleita só lê forte depois de reconstruir o gate; applier parado falha rápido com `503` ([ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)) |
| Configuração | `platform/config` | Sem `DEGRADED_POLICY` a partida falha; sem `DEGRADED_LEASE` só falha com `accept` (com `reject` é ignorada, sem erro); `WRITE_MODE` definida é recusada (proposta a confirmar) ([ADR-053](../03_adr/ADR-053-eventual-sem-apply-paralelo-na-v1.md)) |

## Armadilhas que o desenho já evita

1. **Corpo reutilizado do fasthttp:** o parser copia para o buffer da `Entry` antes de retornar do handler.
2. **Fx escondendo ciclo de dependência:** `ValidateApp` no CI.
3. **Interface grande demais:** cada porta tem menos de dez métodos; se crescer, é sinal de que mistura duas responsabilidades.
4. **Vazamento do Raft para o núcleo:** `depguard` quebra o build.
5. **Log no caminho quente:** só métricas; um teste de benchmark falha se houver alocação no `Write`.
6. **Contexto sem prazo:** toda chamada de porta recebe `context` com deadline herdado da requisição, sempre menor que o timeout de 30 s do SDK ([9.4](../01_arquitetura/08_resiliencia_autocura_e_timeouts.md)).
7. **Waiter que vaza:** `Gate.WaitFor` remove o waiter quando o contexto expira; sob Azure fora, milhares de requisições expiradas não deixam registros.
8. **Gate alimentado só por commit:** o progresso dos learners entra por evento, em goroutine própria; `Append` síncrono do log de replicação não atrasa o gate.
9. **Número de progresso sem termo:** `SetAzureDurable(termo, índice, learner)` ignora o que não é do termo atual ([ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)).
10. **Pânico com lote vazio ou canal fechado:** o `CommitPump` ignora o lote vazio e devolve `ErrStreamClosed`.
11. **Seguir para o `Propose` sem líder:** o `WriteService` espera até `WAIT_LEADER` e responde 503 ([ADR-039](../03_adr/ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md)).
12. **Lease conferido só por temporizador ou contado desde a proposta:** pausa de processo vence o lease sem o temporizador disparar, e uma proposta tardia estenderia a janela. A conferência é a cada ACK (`now < t_c + D`), com relógio monotônico injetável, e a líder nova não herda `t_c`.
13. **Header de modo fixo:** `x-dr-mode` vem do estado real do shard, não de uma função constante.
14. **`WriteTimeout` menor que o prazo da requisição e JSON de erro montado na mão:** o timeout de escrita é `REQUEST_DEADLINE` mais folga e o erro sai de `json.Marshal`.

## Ordem sugerida de implementação

O trabalho segue as **[cinco fases](07_fases_de_trabalho.md)** (decisão 14 da revisão crítica): (1) decisões e contratos de segurança; (2) modelo TLA+ ampliado, que nasce junto com a fase 1; (3) núcleo mínimo verificável (gate, lease, dois logs, um caminho de banco) antes dos seis caminhos de banco; (4) os seis caminhos de banco, o rebuild em tabela sombra e a anti-entropia; (5) conformidade, game days, medição do RTO e prontidão. Dentro das fases 3 e 4, a ordem de construção é:

1. `domain` + `Gate` (com termo) + `Lease` + testes de concorrência (já esboçado) e o modelo TLA+ ampliado.
2. Portas + fakes + `WriteService` + `DegradeService` (escrita ponta a ponta em memória, com lease e política).
3. Adaptador `wal` + suíte de conformidade + `fio` (spike S1).
4. Adaptador `etcdraft` sobre o WAL + conformidade do `ConsensusEngine`; o progresso dos learners sai só das respostas do `AppendEntries`.
5. `dynamohttp` + `Applier` (serial, depois do gate, checkpoint por item) + **um** `DynamoStore` (um nó, depois três). Este é o único caminho de banco da fase 3.
6. `peergrpc` + learners da Azure + gate real.
7. `DegradeController`, `Recovery`, `LeadershipService`.
8. Os seis caminhos de banco: `CosmosStore`, rebuild em tabela sombra, anti-entropia, `drctl`, observabilidade e testes de falha.

## Decisões de biblioteca (Bruno, 12/10)

1. **HTTP: decidido `fasthttp`** atrás da porta, com benchmark contra `net/http` no S1 como verificação.
2. **Logger: decidido `zerolog`.**
3. **Config e env: decidido `viper`** para YAML e env; o peso é medido nos spikes, com `koanf` como plano B.

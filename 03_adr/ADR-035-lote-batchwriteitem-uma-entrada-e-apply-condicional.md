# ADR-035 — BatchWriteItem vira uma entrada; o apply é por item e condicional

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

O `BatchWriteItem` carrega até 25 itens. Se cada item virasse uma entrada, uma falha da líder no meio do lote deixaria parte dele confirmada. Além disso, o `BatchWriteItem` do DynamoDB **não aceita `ConditionExpression`**, e o applier depende da condição de versão (`v < índice`, [ADR-015](ADR-015-wal-post-image-e-versao.md)) para tornar o replay idempotente.

## Decisão

1. O lote vira **uma única entrada** do log: um índice, um ACK, itens em ordem. A versão de todos os itens do lote é esse índice.
2. O applier grava com **`PutItem` e `DeleteItem` condicionais por item**, em paralelo limitado a 25 por entrada. Item já aplicado devolve `ConditionalCheckFailed` e conta como "já aplicado".
3. O ACK do lote só sai com **todos** os itens aplicados na célula da líder. O dedupe é pelo identificador da requisição inteira ([ADR-026](ADR-026-dedupe-de-retry-id-estavel.md)).
4. No Cosmos, o Cloud-Adapter também aplica por item.

## Alternativas consideradas

- **Uma entrada por item:** expõe lote parcial e multiplica índices e ACKs.
- **`TransactWriteItems` no apply:** atômico, mas custa o dobro de unidades de gravação e o limite e a semântica são outros; a v1 não oferece transações ([ADR-025](ADR-025-escopo-da-api-v1-e-sdks.md)).

## Consequências

- Mais chamadas ao banco e P99 maior no ACK do Strong do que com uma chamada de lote. Medir no spike S5.
- Um leitor pode ver o lote pela metade durante o apply, como já ocorre no DynamoDB.
- Um lote cuja soma passa de **4 MiB** (limite de entrada) é recusado antes do commit, mesmo que o DynamoDB aceitasse (o `BatchWriteItem` real chega a 16 MB). Os clientes com itens grandes precisam saber disso.
- Um item que o banco recusa para sempre trava o shard, como em [ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md).

## Atualização de 13/10/2026 (revisão crítica)

O SKIP de um item ruim do lote pula **só o item** `(índice, ordem)`; os demais itens são aplicados ([ADR-048](ADR-048-skip-por-item.md)). O dedupe continua sendo por requisição inteira.

## Onde está o detalhe

- [C23-batchwriteitem-lote-uma-entrada.md](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md)
- [X05-timeouts-e-idempotencia.md](../02_cenarios/complexos/X05-timeouts-e-idempotencia.md)

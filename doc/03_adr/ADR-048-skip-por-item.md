# ADR-048 — O SKIP identifica o item: (índice, ordem)

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026) · **Data:** 13/10/2026

## Contexto

O [ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md) pulava a entrada inteira, inclusive os itens bons de um lote. O DynamoDB já admite sucesso parcial em `BatchWriteItem`. Como o apply é determinístico, todas as células aplicam os mesmos itens e param no mesmo item ruim: não há divergência entre réplicas, mas a semântica de apagar escritas boas era ruim.

## Decisão

1. `SKIP(index, ordem, motivo, autor)` identifica **um item** da entrada. Para uma entrada de um só item, a ordem é 1. Pular a entrada inteira exige listar todas as ordens e continua sendo decisão humana.
2. O applier **persiste o progresso por item** `(índice, ordem)` no volume do log, não no banco. Um replay reaplica os itens anteriores de forma idempotente (condição `v < índice`) e pula só o item marcado.
3. A consulta por identificador da requisição devolve o resultado **por item**: `APPLIED`, `REJECTED` ou `PENDING`.
4. O SKIP continua entrando como entrada replicada, auditada, e a entrada original nunca é apagada.

## Alternativas consideradas

- Pular a entrada inteira, como no ADR-038 original: apaga escritas boas.

## Consequências

- O cliente de um lote com item pulado recebe um resultado parcial na consulta, como no `BatchWriteItem` real.
- A tabela de structs do esboço ganha o progresso por item.

## Onde está o detalhe

- [C26-entrada-venenosa.md](../02_cenarios/atualizados/C26-entrada-venenosa.md)
- [C23-batchwriteitem-lote-uma-entrada.md](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md)

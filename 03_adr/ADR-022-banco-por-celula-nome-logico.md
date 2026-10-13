# ADR-022 — Um banco por célula; nome lógico com {cell}

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

Cada célula tem o seu DynamoDB (ou Cosmos), com tabelas como `cliente_cell_120321`.

## Decisão

O `CELL_ID` vem por variável de ambiente do deploy. O nome lógico é o nome físico com o id trocado por `{cell}`. Há validação de borda, allowlist, `DescribeTable` na partida e a mesma lista de tabelas em todas as células. Um escritor por tabela.

## Alternativas consideradas

Um banco por região; tabela única compartilhada.

## Consequências

Cada escrita lógica é aplicada 6 vezes (custo e throttling): ver R20.

## Atualização de 13/10/2026 (revisão crítica)

**Mantido** pelo Bruno depois da revisão crítica, com o trade-off registrado no [ADR-054](ADR-054-banco-por-celula-mantido.md). O nome lógico passa a ter **geração** para permitir o cutover da reconstrução em tabela sombra ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)): o nome físico pode mudar sem o cliente perceber.

## Onde está o detalhe

- [05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md](../01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md)

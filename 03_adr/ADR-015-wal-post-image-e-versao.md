# ADR-015 — O log guarda a post-image e a versão do item

**Status:** Aceito · **Data:** 11/10/2026

## Contexto

O applier precisa aplicar de forma idempotente e na ordem.

## Decisão

Cada entrada guarda a imagem final do item e a versão (= índice Raft). O applier faz escrita condicional `v < índice`.

## Alternativas consideradas

Guardar a chamada original do SDK: exigiria reexecutar lógica em cada célula.

## Consequências

UpdateItem com expressão e escrita condicional ficam fora da v1 (ADR-025).

## Onde está o detalhe

- [05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md](../01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md)

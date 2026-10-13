# ADR-004 — DynamoDB como primeiro adaptador; Cosmos pelo Cloud-Adapter

**Status:** Confirmado (requisito) · **Data:** 11/10/2026

## Contexto

O banco da AWS é o DynamoDB, e o da Azure é o Cosmos DB.

## Decisão

A API de entrada imita o DynamoDB. A Azure recebe a tradução pelo Cloud-Adapter, que suporta conditional expression e traduz TTL.

## Alternativas consideradas

Uma API própria: obrigaria os clientes a mudar de SDK.

## Consequências

Tipos sem equivalente no Cosmos (Number acima de 15 dígitos, Sets, Binary) seguem como String por padrão (PEND-08).

## Onde está o detalhe

- [05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md](../01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md)

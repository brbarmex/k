# ADR-025 — Escopo da API v1 e SDKs suportados

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

UpdateItem com expressão, condicionais e transações mudam o desenho do log e do applier.

## Decisão

Aceita: PutItem, DeleteItem, BatchWriteItem, GetItem, BatchGetItem, Query, Scan. Recusa UpdateItem com expressão, escrita condicional e TransactWriteItems. SDKs: Java 2.x, Go v2 e .NET v4; identificação por User-Agent (não é segurança); User-Agent não reconhecido é rejeitado depois de 2 a 4 semanas só com alerta; entrada máxima de 4 MiB.

## Alternativas consideradas

Suportar condicionais e transações na v1.

## Consequências

Rejeição é HTTP 400 não retentável. Reavaliar na v1.1.

## Onde está o detalhe

- [09_12factors_cloud_native_e_sdks.md](../01_arquitetura/09_12factors_cloud_native_e_sdks.md)

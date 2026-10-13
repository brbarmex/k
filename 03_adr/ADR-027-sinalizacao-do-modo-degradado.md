# ADR-027 — Modo degradado sinalizado por header e métrica

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

A API é compatível com DynamoDB e não tem campo para avisar o cliente do RPO > 0.

## Decisão

A API segue aceitando escritas e responde com o header `x-dr-mode: degraded`, mais a métrica `degraded_mode`. O cliente decide.

## Alternativas consideradas

Rejeitar escritas durante a degradação (desfaz a opção B).

## Consequências

Os SDKs ignoram o header; apps que o leem precisam de interceptor. PEND-02 passa a bloqueante.

## Onde está o detalhe

- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

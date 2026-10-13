# ADR-008 — Terceiro domínio com WAL integral

**Status:** Descartado · **Data:** 11/10/2026

## Contexto

Opção aberta na V1 para sobreviver à perda de uma nuvem sem decisão humana.

## Decisão

Descartado pela restrição dura (só AWS e Azure em São Paulo).

## Alternativas consideradas

n/a

## Consequências

A promoção da Azure fica humana (ADR-033) e o split-brain é tratado como pendência com os arquitetos (PEND-01).

## Atualização de 13/10/2026 (revisão crítica)

Sem terceiro domínio, a autoridade única na partição vem do tempo (lease) e não de um desempate externo ([ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)).

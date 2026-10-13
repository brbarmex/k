# ADR-011 — Witness = manifesto assinado, snapshots e WAL

**Status:** Mantido · **Data:** 11/10/2026

## Contexto

O termo "witness" era ambíguo na V1.

## Decisão

Witness não tem papel de voto: guarda um manifesto assinado, snapshots e WAL para recuperação.

## Alternativas consideradas

Witness votante: descartado.

## Consequências

Um witness só de metadados não prova RPO 0.

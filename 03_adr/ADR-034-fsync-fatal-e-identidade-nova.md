# ADR-034 — Falha de fsync é fatal; disco perdido volta com identidade nova

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

Um nó que perde o disco pode já ter votado e confirmado entradas.

## Decisão

Falha de fsync encerra o processo (sem retry). Corrupção no meio do log faz o nó recusar subir. Disco perdido volta com identidade nova, como learner. Nunca reusar o ID antigo com log vazio. A reconstrução é manual na v1 (`drctl rebuild`).

## Alternativas consideradas

Retry de fsync; reuso de identidade.

## Consequências

Reconstrução automática fica como hipótese H-DR1.

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md)
- [C21-disco-fsync-e-reconstrucao.md](../02_cenarios/atualizados/C21-disco-fsync-e-reconstrucao.md)

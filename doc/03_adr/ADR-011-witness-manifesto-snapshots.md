# ADR-011 — Witness = manifesto assinado, snapshots e WAL

**Status:** Sem uso (Bruno, 14/10/2026: resposta 3a do inventário); só registro histórico · **Data:** 11/10/2026

## Contexto

O termo "witness" era ambíguo na V1.

## Decisão

Witness não tem papel de voto: guarda um manifesto assinado, snapshots e WAL para recuperação.

## Alternativas consideradas

Witness votante: descartado.

## Consequências

Um witness só de metadados não prova RPO 0.

## Atualização de 14/10/2026 (inventário dos ADRs, resposta 3a)

Nenhum ADR posterior usa o witness. A recuperação vem do log do Raft e da reconstrução em tabela sombra ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)), e o snapshot do Raft é só um marcador ([ADR-063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md)). O ADR fica como registro histórico; se o witness voltar a ter papel, entra por ADR novo.

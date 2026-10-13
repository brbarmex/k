# ADR-009 — Modo Eventual = E1 (mesma escrita, leitura local)

**Status:** Decidido (Bruno) · **Data:** 11/10/2026

## Contexto

Era preciso definir se o modo Eventual alterava a durabilidade da escrita.

## Decisão

E1: a escrita é a mesma nos dois modos (RPO 0); só a leitura muda. O apply é sempre serial e depois do gate (ADR-053; o apply paralelo do ADR-023 saiu da v1).

## Alternativas consideradas

E2: Eventual com durabilidade reduzida. Descartado.

## Consequências

A leitura Eventual informa a defasagem em headers.

## Atualização de 13/10/2026 (revisão crítica)

O apply paralelo no Eventual foi **retirado da v1** ([ADR-053](ADR-053-eventual-sem-apply-paralelo-na-v1.md)): o Eventual é só um modo de **leitura**, e a escrita é a mesma nos dois modos.

## Onde está o detalhe

- [06_modos_de_escrita_e_leitura_e_latencia.md](../01_arquitetura/06_modos_de_escrita_e_leitura_e_latencia.md)

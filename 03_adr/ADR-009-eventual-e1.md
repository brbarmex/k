# ADR-009 — Modo Eventual = E1 (mesma escrita, leitura local)

**Status:** Decidido (Bruno) · **Data:** 11/10/2026

## Contexto

Era preciso definir se o modo Eventual alterava a durabilidade da escrita.

## Decisão

E1: a escrita é a mesma nos dois modos (RPO 0); só a leitura muda. Na v1, o Eventual também permite apply em paralelo (ADR-023).

## Alternativas consideradas

E2: Eventual com durabilidade reduzida. Descartado.

## Consequências

A leitura Eventual informa a defasagem em headers.

## Onde está o detalhe

- [06_modos_de_escrita_e_leitura_e_latencia.md](../01_arquitetura/06_modos_de_escrita_e_leitura_e_latencia.md)

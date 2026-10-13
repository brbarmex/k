# ADR-002 — Shards lógicos, células e ativo/passivo

**Status:** Confirmado (requisito) · **Data:** 10/10/2026

## Contexto

Os dados são divididos em shards, cada um com células em AZs diferentes.

## Decisão

Cada shard tem 3 células na AWS (votantes) e 3 na Azure (learners). Há uma líder por shard; ordenação por shard (decisão de 11/10).

## Alternativas consideradas

Ordenação por chave de partição (adiada para uma fase 2, ver hipótese H11).

## Consequências

Um shard que cresça além da capacidade de um log precisa de procedimento de divisão (hipótese aberta H11).

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)

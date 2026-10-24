# ADR-002 — Shards lógicos, células e ativo/passivo

**Status:** Confirmado (requisito); composição alterada pelo ADR-064 (24/10/2026) · **Data:** 10/10/2026

## Contexto

Os dados são divididos em shards, cada um com células em AZs diferentes.

## Decisão

Cada shard tem 3 células na AWS (votantes) e 3 na Azure (learners). Há uma líder por shard; ordenação por shard (decisão de 11/10).

## Alternativas consideradas

Ordenação por chave de partição (adiada para uma fase 2, ver hipótese H11).

## Consequências

Um shard que cresça além da capacidade de um log precisa de procedimento de divisão (hipótese aberta H11).

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

A composição mudou em 18/10 ([ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)): só AWS com 3 ou 5 células votantes; multicloud com **3 AWS + 2 Azure, todas votantes**, sempre com a maioria na AWS. Não há mais learners. A ordenação por shard continua.

**Na POC:** 3 células AWS votantes, um shard só.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)

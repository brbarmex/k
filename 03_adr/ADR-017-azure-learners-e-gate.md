# ADR-017 — Azure como learners, com gate

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

O Raft não oferece "maioria AWS e maioria Azure" sem prender a eleição.

## Decisão

A Azure (Z1 a Z3) são learners. O Raft decide commit e eleição com os 3 votantes AWS. `gateIndex = min(commit Raft, índice durável em ≥ 2 de 3 learners)` controla ACK e applier.

## Alternativas consideradas

Joint consensus do etcd raft: a eleição exigiria a Azure e a saída do degradado travaria.

## Consequências

O gate é lógica nossa fora de uma prova conhecida: exige modelo TLA+ (ADR-030).

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)

# ADR-017 — Azure como learners, com gate

**Status:** **Substituído pelo ADR-064** (24/10/2026); era Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

O Raft não oferece "maioria AWS e maioria Azure" sem prender a eleição.

## Decisão

A Azure (Z1 a Z3) são learners. O Raft decide commit e eleição com os 3 votantes AWS. `gateIndex = min(commit Raft, índice durável em ≥ 2 de 3 learners)` controla ACK e applier.

## Alternativas consideradas

Joint consensus do etcd raft: a eleição exigiria a Azure e a saída do degradado travaria.

## Consequências

O gate é lógica nossa fora de uma prova conhecida: exige modelo TLA+ (ADR-030).

## Atualização de 13/10/2026 (revisão crítica)

O progresso da Azure só chega pelas respostas do `AppendEntries` e só conta no termo atual da líder; o gate é alimentado por evento ([ADR-046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)).

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

Substituído por [ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md): a Azure tem **2 células votantes sem candidatura**, e o gate exige **K = 1** célula Azure com fsync, não 2 de 3 learners. A regra do [ADR-046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md) (só progresso confirmado pelo Raft no termo atual) continua.

**Na POC:** sem Azure; o gate com learners que existia só em teste de unidade foi removido em 17/10.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)

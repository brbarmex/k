# ADR-006 — Consenso por shard; eleição só na AWS

**Status:** Aceito · **Data:** 11/10/2026

## Contexto

A ordem das escritas precisa de um único líder por shard, e a Azure não pode eleger sozinha (risco de split-brain).

## Decisão

Raft por shard com 3 votantes na AWS. A Azure participa como learner e entra no ACK pelo gate.

## Alternativas consideradas

Joint consensus (AWS e Azure votam): a eleição passaria a exigir a Azure e a saída do modo degradado travaria.

## Consequências

Se duas AZs da AWS caem, o shard para até a promoção humana (cenário R06).

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)
- [R06-duas-azs-aws-caem.md](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md)

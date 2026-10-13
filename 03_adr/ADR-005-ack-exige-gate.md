# ADR-005 — ACK só depois do gate (Raft AWS e maioria durável Azure)

**Status:** Aceito (reformulado na v1) · **Data:** 12/10/2026

## Contexto

RPO 0 em operação normal exige que a escrita esteja durável nas duas nuvens antes da resposta ao cliente.

## Decisão

O ACK só sai quando `gateIndex = min(commit Raft, maioria Azure durável)` alcança o índice da escrita. Ver ADR-017 e ADR-029.

## Alternativas consideradas

ACK com quórum em memória: rejeitado pelo Bruno (risco de falha de software correlacionada nas 6 réplicas).

## Consequências

A latência de escrita inclui o RTT AWS↔Azure e o fsync nas duas pontas.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)
- [C03-escrita-strong.md](../02_cenarios/atualizados/C03-escrita-strong.md)

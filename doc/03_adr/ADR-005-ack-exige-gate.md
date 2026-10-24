# ADR-005 — ACK só depois do gate (Raft AWS e maioria durável Azure)

**Status:** Aceito; regra do gate alterada pelo ADR-064 (24/10/2026) · **Data:** 12/10/2026

## Contexto

RPO 0 em operação normal exige que a escrita esteja durável nas duas nuvens antes da resposta ao cliente.

## Decisão

O ACK só sai quando `gateIndex = min(commit Raft, maioria Azure durável)` alcança o índice da escrita. Ver ADR-017 e ADR-029.

## Alternativas consideradas

ACK com quórum em memória: rejeitado pelo Bruno (risco de falha de software correlacionada nas 6 réplicas).

## Consequências

A latência de escrita inclui o RTT AWS↔Azure e o fsync nas duas pontas.

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

O gate passou a ser `min(commit do Raft, maior índice durável entre as células Azure)`, com **K = 1** célula Azure com fsync ([ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)), e não mais a maioria de 3 learners. Com a Azure fora, a AWS segue sozinha com RPO > 0 sinalizado.

**Na POC:** sem Azure, o gate é o commit do Raft na líder.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)
- [C03-escrita-strong.md](../02_cenarios/atualizados/C03-escrita-strong.md)

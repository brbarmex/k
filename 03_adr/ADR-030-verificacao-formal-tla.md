# ADR-030 — Modelo TLA+ do gate antes do código do gate

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

O gate e o par DEGRADE/RESTORE são lógica nossa fora de uma prova conhecida.

## Decisão

Escrever e verificar um modelo TLA+ de gate, DEGRADE/RESTORE e troca de líder antes de implementar o gate.

## Alternativas consideradas

Só testes de simulação.

## Consequências

Alguns dias de trabalho antes do código do gate.

## Atualização de 13/10/2026

O modelo também deve cobrir a regra candidata "entrar em modo degradado só com 3 de 3 votantes AWS vivos" e compará-la com a regra atual. O Bruno decidiu **decidir essa regra com o resultado do modelo**, em vez de adotá-la antes ([R06](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md)). A decisão final depende da PEND-01.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)

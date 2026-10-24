# ADR-006 — Consenso por shard; eleição só na AWS

**Status:** Aceito; alterado pelo ADR-064 (24/10/2026): a Azure vota, mas não se candidata · **Data:** 11/10/2026

## Contexto

A ordem das escritas precisa de um único líder por shard, e a Azure não pode eleger sozinha (risco de split-brain).

## Decisão

Raft por shard com 3 votantes na AWS. A Azure participa como learner e entra no ACK pelo gate.

## Alternativas consideradas

Joint consensus (AWS e Azure votam): a eleição passaria a exigir a Azure e a saída do modo degradado travaria.

## Consequências

Se duas AZs da AWS caem, o shard para até a promoção humana (cenário R06).

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

A Azure deixou de ser learner: as 2 células Azure **votam**, mas não se candidatam; só viram líder por comando de promoção ([ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)). Com 3 AWS + 2 Azure a eleição não exige a Azure enquanto as 3 AWS estão de pé; com uma AZ AWS fora, exige um voto Azure.

**Na POC:** só os 3 votantes AWS.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)
- [R06-duas-azs-aws-caem.md](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md)

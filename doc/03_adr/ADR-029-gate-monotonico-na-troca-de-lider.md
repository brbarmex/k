# ADR-029 — O gate nunca recua; líder nova reconstrói antes de ACKar

**Status:** Decidido (Bruno); alterado pelos ADRs 064 e 065 (24/10/2026) · **Data:** 12/10/2026

## Contexto

O índice durável da Azure vive na memória da líder; uma líder nova começa sem ele.

## Decisão

A líder nova não ACKa nem libera o applier até consultar os learners e adotar o 2º maior índice durável. O gate é monotônico.

## Alternativas consideradas

Persistir o índice no log Raft (um commit a mais por atualização).

## Consequências

Janela curta sem ACK logo após a eleição.

## Atualização de 13/10/2026 (revisão crítica)

A líder nova só adota progresso **confirmado no termo novo**; valores reportados fora de banda são ignorados até a primeira confirmação ([ADR-046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)). A líder recém-eleita também só atende leitura forte depois de reconstruir o gate ([ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md)).

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

A regra (o gate nunca recua) continua, com dois ajustes: o índice confirmado vai no snapshot-marcador e restaura o gate depois de um reinício ([ADR-065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md), G6); e, no multicloud, a líder nova reconstrói o gate pelo `Match` das 2 células Azure votantes no termo novo, com K = 1 ([ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)).

**Na POC:** `Gate.Restore` com o confirmado do marcador na partida; sem Azure.

## Onde está o detalhe

- [X01-gate-e-troca-de-lider.md](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md)

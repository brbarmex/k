# ADR-029 — O gate nunca recua; líder nova reconstrói antes de ACKar

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

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

## Onde está o detalhe

- [X01-gate-e-troca-de-lider.md](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md)

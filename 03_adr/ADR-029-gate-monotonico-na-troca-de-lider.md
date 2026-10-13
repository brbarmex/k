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

## Onde está o detalhe

- [X01-gate-e-troca-de-lider.md](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md)

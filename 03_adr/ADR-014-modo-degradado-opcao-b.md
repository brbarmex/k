# ADR-014 — Modo degradado: a AWS segue quando a Azure fica inalcançável (opção B)

**Status:** Decidido (Bruno, 11/10/2026) · **Data:** 11/10/2026

## Contexto

Se a Azure ou o link cai, parar as escritas violaria a disponibilidade; seguir quebra o RPO 0.

## Decisão

Depois de `DEGRADE_AFTER`, a líder propõe a entrada replicada `DEGRADE(epoch, fromIndex)` e confirma só com a maioria AWS (RPO > 0). A saída é `RESTORE(epoch)` com histerese. Sinalização ao cliente no ADR-027.

## Alternativas consideradas

Opção A: parar as escritas até a Azure voltar. Rejeitada pelo Bruno.

## Consequências

O RPO é maior que 0 durante a degradação (PEND-02, bloqueante). A promoção da Azure durante a degradação perde dado declarado (cenário C15).

## Onde está o detalhe

- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

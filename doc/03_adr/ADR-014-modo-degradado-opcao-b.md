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

## Atualização de 13/10/2026 (revisão crítica)

O modo degradado passou a ter **validade (lease) de duração D**: ao fim dela a AWS para de aceitar escrita sozinha, e a Azure só pode ser promovida depois de D mais margem ([ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)). Além disso, cada instalação escolhe `DEGRADED_POLICY=accept` (esta opção B, com lease) ou `reject` (sem degradar, RPO 0 sempre) ([ADR-045](ADR-045-politica-de-degradacao-por-instalacao.md)).

## Onde está o detalhe

- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

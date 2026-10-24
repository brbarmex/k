# ADR-027 — Modo degradado sinalizado por header e métrica

**Status:** Decidido (Bruno); alterado pelo ADR-064 (24/10/2026) · **Data:** 12/10/2026

## Contexto

A API é compatível com DynamoDB e não tem campo para avisar o cliente do RPO > 0.

## Decisão

A API segue aceitando escritas e responde com o header `x-dr-mode: degraded`, mais a métrica `degraded_mode`. O cliente decide.

## Alternativas consideradas

Rejeitar escritas durante a degradação (desfaz a opção B).

## Consequências

Os SDKs ignoram o header; apps que o leem precisam de interceptor. PEND-02 passa a bloqueante.

## Atualização de 13/10/2026 (revisão crítica)

O header **não é o aceite de risco**: os SDKs o ignoram. O aceite é a variável `DEGRADED_POLICY` por instalação ([ADR-045](ADR-045-politica-de-degradacao-por-instalacao.md)), e o modo degradado tem validade ([ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)). Ao fim do lease a resposta é `503 LEASE_EXPIRED`.

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

O header `x-dr-mode` e a métrica continuam sendo o sinal de RPO > 0 quando a AWS segue sem a Azure ([ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)). Saem o `503 LEASE_EXPIRED` e o aceite pela `DEGRADED_POLICY`, que deixaram de existir.

**Na POC:** não implementado (não há Azure).

## Onde está o detalhe

- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

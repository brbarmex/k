# ADR-026 — Dedupe de retry na líder por identificador estável

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

O retry do SDK chega como chamada nova.

## Decisão

A líder deduplica por identificador estável do SDK quando existir; usa o hash (tabela, chave, conteúdo) só como último recurso. Janela de 5 min (3 tentativas de 30 s). O timeout do SDK é maior que o deadline do DataReplication.

## Alternativas consideradas

Dedupe no DataReplication (não sobrevive ao reinício).

## Consequências

Falso positivo do hash em escritas iguais legítimas; memória da janela (R22); o lote é uma entrada e o dedupe é pelo id da requisição (ADR-035, C23).

## Atualização de 13/10/2026 (revisão crítica)

O limite fica explícito: fora da janela de 5 min vale a semântica do DynamoDB, e a escrita condicional está fora da v1. O levantamento do uso real de `ConditionExpression` nos clientes é obrigatório ([ADR-051](ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)). O dedupe é **por requisição**, não por item (a tabela de structs foi corrigida).

## Onde está o detalhe

- [C07-timeout-depois-do-commit.md](../02_cenarios/atualizados/C07-timeout-depois-do-commit.md)

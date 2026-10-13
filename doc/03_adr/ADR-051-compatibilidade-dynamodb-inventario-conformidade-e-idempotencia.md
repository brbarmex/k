# ADR-051 — Compatibilidade com o DynamoDB: inventário, conformidade e limite da idempotência

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026) · **Data:** 13/10/2026

## Contexto

O recorte da API da v1 ([ADR-025](ADR-025-escopo-da-api-v1-e-sdks.md)) é uma decisão, não um defeito. Faltam o inventário real de chamadas, uma suíte que compare comportamentos e a declaração honesta do limite da idempotência. Um `PutItem` cego repetido depois da janela de 5 min sobrescreve uma versão mais nova também no DynamoDB; a camada não piora isso além da janela de dedupe ([ADR-026](ADR-026-dedupe-de-retry-id-estavel.md)). O defeito real é outro: **a escrita condicional está fora da v1**, e uma aplicação que usa concorrência otimista (versão, saldo) não tem como se proteger pela camada.

## Decisão

1. **Documentar o limite:** dedupe por requisição na janela de 5 min; fora dela vale a semântica do DynamoDB. Sem identificador durável de operação de negócio na v1 (extensão de API não adotada).
2. **Levantar o uso real de `ConditionExpression`** (e de `UpdateItem` com expressão e transações) em cada cliente antes de ele entrar, a partir de eventos de dados do CloudTrail ou dos logs do SDK. Se um cliente depender deles, a decisão de antecipar a v1.1 volta ao Bruno.
3. **Inventário real de chamadas e parâmetros** por cliente, antes de prometer "só trocar o endpoint".
4. **Suíte de conformidade** que roda a mesma sequência contra o DynamoDB real, contra a camada e contra o Cosmos pelo Cloud-Adapter, e compara os resultados (erros, paginação, tamanhos, tipos numéricos, TTL).
5. **`_dr_v` nunca aparece nas respostas** (`GetItem`, `Query`, `Scan`, projeções), com teste.

## Alternativas consideradas

- Um identificador durável de operação de negócio como extensão da API: fora do contrato DynamoDB.

## Consequências

- Nenhum cliente entra sem inventário e sem a suíte verde para o seu uso.
- A semântica do Cosmos depois do failover (tamanho de item, precisão de `Number`, índices consistentes, TTL) entra na suíte, não só a do DynamoDB.

## Onde está o detalhe

- [X05-timeouts-e-idempotencia.md](../02_cenarios/complexos/X05-timeouts-e-idempotencia.md)
- [R17-atributo-de-versao-e-limite-400kb.md](../02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md)

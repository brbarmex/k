# ADR-050 — `ConsistentRead=true` em GSI é recusada, como no DynamoDB

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026) · **Data:** 13/10/2026

## Contexto

O [ADR-024](ADR-024-delete-real-ttl-local-gsi.md) dizia "leitura forte em GSI até a propagação do índice", o que sugere uma garantia que a camada não tem como dar. O DynamoDB só oferece leitura eventual em GSI. Depois do failover há ainda uma assimetria: o índice do Cosmos é consistente por padrão e o do DynamoDB não.

## Decisão

1. `ConsistentRead=true` em `Query` de **GSI** é recusada com o mesmo erro do DynamoDB (`ValidationException`, HTTP 400).
2. Sem o parâmetro, `READ_MODE_DEFAULT=strong` vale **só para a tabela base**. Em GSI a leitura é sempre eventual, com o header `x-dr-consistency: eventual`.
3. A aplicação não deve depender da consistência do índice em nenhuma das duas nuvens.

## Alternativas consideradas

- Manter "forte até propagar": promessa que a camada não cumpre.

## Consequências

- Aplicações que hoje usam `ConsistentRead` em GSI já falham no DynamoDB; nada muda para elas.
- O contrato do Cloud-Adapter inclui um teste de que o Cosmos não "melhora" o índice a ponto de a aplicação passar a depender disso.

## Onde está o detalhe

- [X06-delete-ttl-gsi.md](../02_cenarios/complexos/X06-delete-ttl-gsi.md)

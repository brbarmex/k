# ADR-050 — `Query` em GSI é recusada

**Status:** Decidido (Bruno, 13/10/2026; revisa a decisão da revisão crítica, que mantinha a leitura eventual em GSI) · **Data:** 13/10/2026

## Contexto

O `DataReplication` tem um único modo, strong consistency ([ADR-057](ADR-057-um-unico-modo-strong-consistency.md)), e as aplicações sempre enviam `ConsistentRead=true`. O índice global secundário (GSI) do DynamoDB é atualizado de forma assíncrona e **não oferece leitura forte**: o próprio DynamoDB recusa `ConsistentRead=true` em GSI. Depois do failover há ainda uma assimetria: o índice do Cosmos é consistente por padrão e o do DynamoDB não. Uma Query em GSI seria a única leitura sem garantia forte do sistema.

## Decisão

1. **Toda `Query` com `IndexName` de um GSI é recusada** com `ValidationException` (HTTP 400), com ou sem `ConsistentRead`, nas células AWS e Azure.
2. A camada não oferece nenhuma leitura fora da barreira forte. Quem hoje consulta por GSI passa a consultar a tabela base pela chave.
3. `Scan` com `IndexName` de GSI também é recusado, pela mesma razão.

## Alternativas consideradas

- **Manter o GSI como exceção documentada** (consistência do índice do DynamoDB, com header): mantinha as aplicações que usam GSI funcionando, mas criava a única leitura sem garantia forte. Descartada pelo Bruno.
- **Esperar a barreira forte antes de consultar o índice:** reduz a defasagem, mas o índice continua atrasando; seria uma promessa que a camada não cumpre.

## Consequências

- **As aplicações que usam Query em GSI (algumas, pelo levantamento de 12/10) precisam mudar o padrão de acesso** antes de entrar na camada: consulta pela chave da tabela base ou modelagem nova. Entra no inventário de chamadas (PEND-15) e na suíte de conformidade (PEND-16) como chamada recusada.
- **GSIs existentes ficam sem uso pela camada.** Cada GSI ainda consome escrita nas seis tabelas (3 DynamoDB e 3 contêineres Cosmos com índice). Recomendação: removê-los das tabelas atendidas pela camada; a tabela sombra do rebuild ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)) é criada sem GSIs.
- Some a assimetria de índice entre DynamoDB e Cosmos depois do failover: não há leitura por índice em nenhuma das nuvens.
- Métrica: `rejected_by_reason{reason="gsi"}`, para achar quem ainda tenta.

## Onde está o detalhe

- [X06-delete-ttl-gsi.md](../02_cenarios/complexos/X06-delete-ttl-gsi.md)
- [C05-leitura-strong.md](../02_cenarios/atualizados/C05-leitura-strong.md)

# ADR-036 — O SLO de 50 ms exclui a escrita no banco e é publicado como "overhead + banco"

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

A meta de overhead P99 ≤ 50 ms deixava a escrita no DynamoDB fora da soma. Só que o ACK sai depois do apply na célula da líder ([ADR-057](ADR-057-um-unico-modo-strong-consistency.md)), então a latência do DynamoDB, com a cauda dele, está no caminho do ACK.

## Decisão

O SLO de 50 ms **continua excluindo** a escrita no DynamoDB (`t_apply`). Ao cliente, a latência é publicada como **`overhead + escrita no banco`**, com o P99 do banco como parte do contrato. Os tempos `t_parse`, `t_raft`, `t_azure` e `t_apply` são medidos separadamente.

## Alternativas consideradas

- **Incluir a escrita no banco nos 50 ms:** o orçamento só fecha se o P99 do DynamoDB for muito baixo.
- **ACK antes do apply na célula da líder:** reduz a latência, mas quebra a leitura-do-que-escrevi da leitura forte local e reabre o [ADR-057](ADR-057-um-unico-modo-strong-consistency.md).

## Consequências

- A latência soma `t_parse + max(t_raft, t_azure) + t_apply + t_resp`: o apply é sempre serial depois do gate (modo único strong, [ADR-057](ADR-057-um-unico-modo-strong-consistency.md)).
- Throttling do DynamoDB por mais que o prazo vira `503 PENDING` ([ADR-028](ADR-028-timeouts-em-cascata.md)).
- O alvo de 150 ms de escrita P99 do cliente continua valendo ponta a ponta; os números fecham no spike S1.

## Onde está o detalhe

- [C24-orcamento-de-50ms-e-escrita-no-dynamodb.md](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md)

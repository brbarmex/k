# ADR-038 — O SKIP de uma entrada venenosa é sempre aprovado por uma pessoa

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

Com apply serial por shard no Strong, o applier não pode pular uma entrada. Uma entrada já commitada que o banco recusa para sempre trava o shard.

## Decisão

1. **Validar antes do commit** contra as regras dos dois bancos (DynamoDB e Cosmos). O que nenhum aceita nunca vira entrada do log.
2. **Classificar o erro do apply:** transitório (throttling, 5xx, timeout) tenta de novo; permanente para o applier com parada explícita, métrica `poison_entry_index` e alerta.
3. **`SKIP(index, motivo, autor)` é decidido sempre por uma pessoa** e entra como entrada replicada: todas as células pulam a mesma entrada, a divergência fica registrada e a entrada original nunca é apagada.

## Alternativas consideradas

- **SKIP automático para classes determinísticas** (H-DR2): reduz o tempo parado, mas apaga escritas sem decisão humana. Essas classes deveriam ter sido recusadas na validação do passo 1.

## Consequências

- O shard fica parado até alguém agir. Em segundos, todas as escritas do shard viram `503 PENDING`: precisa de plantão e de um circuito que rejeite rápido enquanto o shard está parado.
- A consulta por identificador precisa devolver `REJECTED` para a escrita pulada.
- Recusa que só existe no Cosmos é silenciosa até o failover: métrica `applied_lag` por cloud e teste de contrato do Cloud-Adapter.

## Onde está o detalhe

- [C26-entrada-venenosa.md](../02_cenarios/atualizados/C26-entrada-venenosa.md)

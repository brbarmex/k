# ADR-049 — A leitura forte espera `min(ReadIndex, gate)`

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026) · **Data:** 13/10/2026

## Contexto

A leitura forte esperava `applied >= ReadIndex`, com o `ReadIndex` do commit Raft. No modo Strong o apply só ocorre depois do gate, então **uma Azure lenta atrasava as leituras fortes**, mesmo sem escrita pendente. A disponibilidade de leitura ficava acoplada à saúde do link, justamente na faixa cinzenta ([C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md)). Uma entrada pendente ou venenosa também bloqueava a leitura forte do shard inteiro.

## Decisão

1. A barreira da leitura forte é **`min(ReadIndex, gate)`**: o cliente só pode ter visto escritas já ACKadas, e toda escrita ACKada está abaixo do gate.
2. Na célula follower, a líder devolve **o `ReadIndex` e o gate no mesmo momento** (mesma resposta). Um gate antigo guardado na follower enfraqueceria a garantia.
3. A líder recém-eleita só atende leitura forte depois de reconstruir o gate ([ADR-029](ADR-029-gate-monotonico-na-troca-de-lider.md), [ADR-046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)).
4. Com o applier parado por entrada venenosa, a leitura forte do shard falha rápido com `503` em vez de esperar até o timeout.

## Alternativas consideradas

- Como estava: acopla leitura à Azure.

## Consequências

- Requer que `applied` nunca passe do gate ([ADR-057](ADR-057-um-unico-modo-strong-consistency.md)), senão a leitura veria escrita não ACKada.
- A leitura-do-que-escrevi continua garantida.

## Onde está o detalhe

- [C05-leitura-strong.md](../02_cenarios/atualizados/C05-leitura-strong.md)
- [R16-leitura-forte-em-celulas-follower.md](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md)

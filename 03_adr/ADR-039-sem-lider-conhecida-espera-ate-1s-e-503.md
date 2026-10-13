# ADR-039 — Sem líder conhecida: esperar até 1 s no servidor e então 503

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

Em uma eleição, células diferentes podem ter visões diferentes de quem é a líder. Sem regra, um encaminhamento circula entre follower e follower até estourar o prazo ([ADR-010](ADR-010-follower-encaminha-para-lider.md)).

## Decisão

1. Uma requisição já encaminhada **nunca** é reencaminhada (limite de um salto).
2. O encaminhamento leva o **termo** conhecido; quem recebe um termo maior atualiza a visão em vez de reencaminhar.
3. Sem líder conhecida, o servidor **espera até `WAIT_LEADER` (1 s)** pela eleição e depois responde `503` com `retryAfter`.
4. O identificador estável de dedupe viaja junto ([ADR-026](ADR-026-dedupe-de-retry-id-estavel.md)).

## Alternativas consideradas

- **503 imediato:** simples, mas gasta as 3 tentativas do SDK dentro de uma eleição curta.

## Consequências

- A espera cabe no limite de 3 s do encaminhamento ([ADR-028](ADR-028-timeouts-em-cascata.md)).
- Durante uma eleição, as requisições esperando ocupam memória e conexões: o número de requisições aguardando líder precisa de teto.
- `WAIT_LEADER` entra na verificação de prazos da partida.

## Onde está o detalhe

- [C27-encaminhamento-sem-lider-conhecida.md](../02_cenarios/atualizados/C27-encaminhamento-sem-lider-conhecida.md)
- [C06-follower-encaminha.md](../02_cenarios/atualizados/C06-follower-encaminha.md)

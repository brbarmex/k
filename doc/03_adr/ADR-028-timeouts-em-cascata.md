# ADR-028 — Prazos em cascata dentro do timeout do SDK

**Status:** Decidido (Bruno); valores a validar · **Data:** 13/10/2026

## Contexto

O timeout do SDK é de 30 s por tentativa, com no máximo 3 tentativas.

## Decisão

`REQUEST_DEADLINE = CLIENT_TIMEOUT/4 = 7,5 s`; `DEGRADE_AFTER = 2 s`; forward para a líder até 3 s; cliente do banco com 1 s por tentativa e no máximo 2. O prazo nasce na borda e desce como `context`. A partida recusa subir com prazos fora de ordem. `IdleTimeout` do servidor de 120 s.

## Alternativas consideradas

Responder perto dos 30 s.

## Consequências

Prazo estourado depois do commit devolve `503 PENDING`; o retry é absorvido pelo dedupe e pela versão.

## Atualização de 14/10/2026 (inventário dos ADRs, resposta 5a)

O limite do **cliente do banco** (1 s por tentativa, no máximo 2) vale só **no caminho da requisição**: o que a requisição faz no banco dentro do `REQUEST_DEADLINE`, como o `GetItem` e o `Scan` da leitura forte. O **applier** repete erro transitório **sem limite**, com espera exponencial de 100 ms a 5 s ([ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md), AP-07 da spec da POC): a entrada já está commitada, e desistir dela travaria o shard do mesmo jeito. O limite do applier é o tempo, visível em `dr_apply_lag` e na admissão por atraso ([ADR-060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)).

## Onde está o detalhe

- [08_resiliencia_autocura_e_timeouts.md](../01_arquitetura/08_resiliencia_autocura_e_timeouts.md)

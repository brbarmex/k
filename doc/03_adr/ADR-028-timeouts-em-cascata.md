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

## Onde está o detalhe

- [08_resiliencia_autocura_e_timeouts.md](../01_arquitetura/08_resiliencia_autocura_e_timeouts.md)

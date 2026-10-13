# ADR-016 — Um único app DataReplication por célula, monolito modular

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

Com 10 microsserviços por cliente, um sidecar em cada pod faria todos participarem do consenso.

## Decisão

Um único app `DataReplication` por célula (API, Raft, log, applier), no namespace do cliente, sem sidecar e sem HPA (o número de réplicas é o quórum). Monolito modular com fronteiras por interface.

## Alternativas consideradas

Sidecar por pod; proxy fino + nó de consenso (continua possível como evolução, seção 3.4).

## Consequências

O pod é ponto único por célula: reinício deixa as apps sem endpoint (mitigações em 3.3). A CPU é compartilhada entre parse e Raft (H-DR6).

## Onde está o detalhe

- [01_forma_do_app_e_escopo.md](../01_arquitetura/01_forma_do_app_e_escopo.md)

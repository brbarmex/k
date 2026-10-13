# ADR-032 — Parse completo do JSON na v1; Rust como alternativa registrada

**Status:** Decidido (Bruno); problema conhecido · **Data:** 12/10/2026

## Contexto

Parsear o JSON de 10 apps disputa CPU com fsync e heartbeats.

## Decisão

Parse completo na v1 (hipótese H-DR6 aceita). A ideia de gravar o corpo bruto e parsear só na restauração foi analisada e adiada. Rust fica como alternativa se o GC afetar heartbeat ou P99, se o parse não couber ou se a memória estourar.

## Alternativas consideradas

Corpo bruto com extração parcial de TableName.

## Consequências

Será resolvido depois dos testes de carga.

## Onde está o detalhe

- [01_forma_do_app_e_escopo.md](../01_arquitetura/01_forma_do_app_e_escopo.md)

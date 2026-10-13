# ADR-031 — Arquitetura hexagonal e bibliotecas

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

Era preciso uma estrutura de código que isole o Raft, o banco e o protocolo.

## Decisão

Go 1.27, portas e adaptadores com arquitetura gritante, Fx (DI, só em `bootstrap`), fasthttp (API, atrás de porta; benchmark no S1), grpc-go com vtprotobuf, zerolog, viper (config e env; plano B koanf). `depguard` impõe as fronteiras.

## Alternativas consideradas

net/http, slog, wire.

## Consequências

fasthttp reutiliza o corpo da requisição e não fala HTTP/2: cuidado no adaptador.

## Onde está o detalhe

- [README.md](../05_implementacao/README.md)

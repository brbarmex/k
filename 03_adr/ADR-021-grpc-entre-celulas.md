# ADR-021 — gRPC entre as células

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

As células trocam replicação, forward e heartbeats.

## Decisão

gRPC com streams bidirecionais por par de nós, canais separados para replicação e para forward, sem compressão, mTLS. A API para as aplicações continua HTTP compatível com DynamoDB.

## Alternativas consideradas

REST/JSON, TCP próprio, QUIC.

## Consequências

Cuidado com keepalive, tamanho de mensagem e multiplexação (seção 4.3).

## Onde está o detalhe

- [02_comunicacao_grpc.md](../01_arquitetura/02_comunicacao_grpc.md)

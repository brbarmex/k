# ADR-020 — Raft: etcd-io/raft com camada multi-grupo própria

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

Era preciso escolher entre etcd-io/raft e Dragonboat.

## Decisão

`go.etcd.io/raft/v3` com uma camada multi-grupo fina nossa. Importado só em `adapter/out/etcdraft`. Dragonboat é o plano B.

## Alternativas consideradas

Dragonboat (traz log e transporte próprios); hashicorp/raft (um grupo por instância); Raft do zero.

## Consequências

Escrever o laço de `Ready`, o transporte gRPC e a camada multi-grupo. A atividade dos repositórios é verificada no spike S6.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)

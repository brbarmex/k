# ADR-018 — Dois logs separados: o do Raft e o de replicação

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

O log de replicação não pode ficar preso à biblioteca Raft, para permitir trocar ou evoluir o consenso.

## Decisão

O log do Raft é interno e de vida curta (fsync antes do ACK). O log de replicação é nosso, de vida longa, gravado depois do commit em lote. O Raft só é truncado até o que já está durável no log de replicação. Na partida, o log de replicação é completado a partir do Raft.

## Alternativas consideradas

Um único log compartilhado (L2 físico): menos escrita, mas acopla a biblioteca.

## Consequências

Escrita em dobro por réplica; o segundo fsync pode piorar a cauda do primeiro (mitigação: lote maior ou volume separado).

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)
- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md)

# ADR-018 — Dois logs separados: o do Raft e o de replicação

**Status:** Decidido (Bruno) · **Data:** 12/10/2026 · **Revisado em 13/10/2026 por [ADR-055](ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)** (log de replicação em Pebble, sem fsync, derivado do Raft)

## Contexto

O log de replicação não pode ficar preso à biblioteca Raft, para permitir trocar ou evoluir o consenso.

## Decisão

O log do Raft é interno e de vida curta (fsync antes do ACK). O log de replicação é nosso, de vida longa, gravado depois do commit em lote. Na partida, o log de replicação é completado a partir do Raft.

**Regra de truncamento (precisada em 13/10 pelo ADR-055):** o Raft só é truncado até o `DurableIndex` do log de replicação. Desde o ADR-055 o log de replicação grava sem fsync, então `DurableIndex` passa a ser **o maior índice coberto por um checkpoint com sync do Pebble**, e não o índice apenas copiado. A retenção para a Azure atrasada fica no log de replicação; o log do Raft guarda só uma folga limitada (`RAFT_RETAIN_MAX`) para seguidores e learners pouco atrasados.

> **Ambiguidade da versão de 12/10.** "Durável no log de replicação" só fazia sentido porque aquele log fazia fsync. Com o Pebble sem fsync, ler a regra como "até o índice copiado" abriria uma janela de perda num nó que cai com o Raft já truncado. Ler como "não truncar o Raft antes do ACK da Azure" levaria a retenção longa do modo degradado para o Raft (e para a memória do `MemoryStorage`), contradizendo o C29. A regra correta é a do ADR-055: **o Raft obedece ao checkpoint do Pebble; o Pebble obedece à Azure.**

## Alternativas consideradas

Um único log compartilhado (L2 físico): menos escrita, mas acopla a biblioteca.

## Consequências

Escrita em dobro por réplica. Desde o ADR-055 o segundo log não faz fsync por entrada e fica em volume separado, então não piora a cauda do primeiro; o custo passa a ser um checkpoint com sync a cada `PEBBLE_SYNC_INTERVAL` no outro volume e a compactação do Pebble.

## Onde está o detalhe

- [ADR-055](ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)
- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)
- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md)

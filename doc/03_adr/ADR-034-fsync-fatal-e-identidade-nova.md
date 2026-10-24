# ADR-034 — Falha de fsync é fatal; disco perdido volta com identidade nova

**Status:** Decidido (Bruno); vigente em parte, alterado pelos ADRs 066 e 067 (24/10/2026) · **Data:** 12/10/2026

## Contexto

Um nó que perde o disco pode já ter votado e confirmado entradas.

## Decisão

Falha de fsync encerra o processo (sem retry). Corrupção no meio do log faz o nó recusar subir. Disco perdido volta com identidade nova, como learner. Nunca reusar o ID antigo com log vazio. A reconstrução é manual na v1 (`drctl rebuild`).

## Alternativas consideradas

Retry de fsync; reuso de identidade.

## Consequências

Reconstrução automática fica como hipótese H-DR1.

## Atualização de 13/10/2026 (ADR-056)

Com group commit, uma falha de escrita ou de fsync encerra o processo **sem entregar nenhuma resposta do lote** (nem ACK, nem voto, nem `MsgAppResp`). Vale para a gravação do log do Raft nos modos síncrono e assíncrono ([ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)).


## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

O fsync fatal continua. Mudou o resto: o volume perdido não volta "como learner" com identidade nova por procedimento manual; até a ConfChange existir, a célula com o volume vazio num grupo que já existe **não sobe** ([ADR-067](ADR-067-membros-pelo-log-do-raft-e-fail-fast-do-volume-perdido.md), DC-12). A reconstrução deixou de ser manual (`drctl rebuild`): é automática para a réplica fora da janela e manual por `POST /admin/reconstruir` ([ADR-066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md)).

**Na POC:** fsync fatal sim; fail-fast do volume perdido implementado na árvore local (sem commit); corrupção no meio do log recusa a partida; cauda rasgada com dados também recusa (sem `wal.Repair`).

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md)
- [C21-disco-fsync-e-reconstrucao.md](../02_cenarios/atualizados/C21-disco-fsync-e-reconstrucao.md)

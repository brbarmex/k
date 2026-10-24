# ADR-019 — Log do Raft em volume de bloco dedicado; sem ACK em memória

**Status:** Decidido (Bruno); retenção alterada pelo ADR-065 (24/10/2026) · **Data:** 12/10/2026 · **Revisado em 13/10/2026:** o WAL próprio (W2) saiu; o log do Raft usa o pacote `wal` do etcd, num volume dedicado por nó. *(O nome do arquivo foi mantido para não quebrar links.)*

## Contexto

O fsync é o gargalo da escrita.

## Decisão

- **Log do Raft**, o único fsync no caminho do ACK: pacote `wal` do etcd, um por shard, com segmentos pré-alocados, `fdatasync` e CRC, em **volume dedicado e provisionado** só para ele (EBS io2/gp3, Azure Premium SSD v2 ou Ultra). Falha de fsync é fatal. NVMe local não será testado.
- **Um volume dedicado por nó**, nas duas nuvens, para o log do Raft. É o único log do nó: a retenção para a Azure atrasada fica nele, com o teto de 70% do [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), e o progresso do applier (registro pequeno com CRC, sem fsync) fica no mesmo volume.
- *Até 13/10 a decisão era um WAL próprio, segmentado, com group commit entre shards. Foi substituída pelo `wal` do etcd, um por shard.*

## Alternativas consideradas

ACK com quórum em memória e memória durável (M1 a M4); WAL próprio segmentado (W2, o desenho anterior); NVMe local.

## Consequências

- O piso do fsync é o do volume de rede; o spike S1 prova o P99, agora com **um fsync por shard** em paralelo (sem group commit entre shards).
- Um volume por nó nas duas nuvens. Ele é dimensionado pela retenção para a Azure no modo degradado (teto de 70%, ADR-041), não só pelo caminho do ACK.

## Atualização de 13/10/2026 (ADR-056)

O volume do log do Raft passa a ter **IOPS provisionadas** acima da base (io2 ou gp3 provisionado; Premium SSD v2 com IOPS acima da base), por causa de um fsync por shard. O group commit é por shard; o compartilhado entre shards volta só pelo gatilho do S1 ([ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)).


## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

O teto de 70% do volume para a retenção da Azure saiu ([ADR-065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md)): a retenção passa a ser uma janela em bytes por réplica (`RETAIN_MAX_BYTES`) com teto duro (`RETAIN_DISK_HARD`, 85%), e a RAM tem teto próprio (`RAFT_RAM_MAX`). O volume dedicado, o `wal` do etcd e o fsync fatal continuam.

**Na POC:** um volume por célula; o progresso do applier e o arquivo `snapshot` ficam no mesmo volume.

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md) (6.0, 6.9)
- [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)

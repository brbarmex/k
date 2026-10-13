# ADR-019 — Log do Raft em volume de bloco dedicado; sem ACK em memória

**Status:** Decidido (Bruno) · **Data:** 12/10/2026 · **Revisado em 13/10/2026 por [ADR-055](ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md):** o WAL próprio (W2) saiu; o log do Raft usa o pacote `wal` do etcd e o log de replicação usa o Pebble, cada um no seu volume. *(O nome do arquivo foi mantido para não quebrar links.)*

## Contexto

O fsync é o gargalo da escrita.

## Decisão

- **Log do Raft**, o único fsync no caminho do ACK: pacote `wal` do etcd, um por shard, com segmentos pré-alocados, `fdatasync` e CRC, em **volume dedicado e provisionado** só para ele (EBS io2/gp3, Azure Premium SSD v2 ou Ultra). Falha de fsync é fatal. NVMe local não será testado.
- **Log de replicação:** Pebble em **um segundo volume** por nó, sem fsync no caminho do ACK ([ADR-055](ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)).
- *Até 13/10 a decisão era um WAL próprio, segmentado, com group commit entre shards, para os dois logs. Foi substituída pelo ADR-055.*

## Alternativas consideradas

ACK com quórum em memória e memória durável (M1 a M4); WAL próprio segmentado (W2, o desenho anterior); NVMe local.

## Consequências

- O piso do fsync é o do volume de rede; o spike S1 prova o P99, agora com **um fsync por shard** em paralelo (sem group commit entre shards).
- Dois volumes por nó nas duas nuvens; o do Raft pode ser pequeno.

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md)
- [ADR-055](ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)

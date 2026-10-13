# ADR-019 — WAL próprio em volume de bloco dedicado; sem ACK em memória

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

O fsync é o gargalo da escrita.

## Decisão

WAL próprio, segmentado e pré-alocado, com group commit natural (sem linger fixo), `fdatasync` e CRC32C, em volume dedicado e provisionado (EBS io2/gp3, Azure Premium SSD v2 ou Ultra). Falha de fsync é fatal. NVMe local não será testado.

## Alternativas consideradas

ACK com quórum em memória e memória durável (M1 a M4); RocksDB ou outro banco como log; NVMe local.

## Consequências

O piso do fsync é o do volume de rede; o spike S1 prova o P99.

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md)

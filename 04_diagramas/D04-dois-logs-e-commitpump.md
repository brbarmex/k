# D04 — Os dois logs e o CommitPump

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

O **log do Raft** é curto e tem fsync antes do ACK. O **log de replicação** é nosso, longo, gravado em lote depois do commit, e serve a Azure atrasada, o `drctl` e a reconstrução. O `CommitPump` liga o commit ao gate, ao applier (pela memória) e ao log de replicação.

## Caminho do dado em um nó

```mermaid
flowchart TD
    W["WriteService: Propose"] --> RAFT["RaftNode"]
    RAFT -->|"fsync antes do ACK"| RLOG[("log do Raft<br/>vida curta")]
    RAFT -->|"commit"| PUMP["CommitPump"]
    PUMP -->|"índice de commit"| GATE["Gate"]
    PUMP -->|"pela memória"| APPL["Applier"]
    PUMP -->|"em lote, fora do caminho do ACK"| REP[("log de replicação<br/>vida longa")]
    GATE -->|"libera ACK e apply"| W
    APPL -->|"escrita condicional por versão"| DB[("Banco da célula")]
    REP -.->|"DurableIndex: até onde o log do Raft pode ser truncado"| RLOG
```

## Onde é detalhado

- Desenho: [03_consenso_raft_quorum_e_gate](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.5 e 5.5.1) e [ADR-018](../03_adr/ADR-018-dois-logs-raft-e-replicacao.md).
- Retenção e fsync: [04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md).
- Coerência na partida: [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md).

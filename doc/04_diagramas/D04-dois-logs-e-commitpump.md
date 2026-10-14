# D04 — Os dois logs e o CommitPump

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

O **log do Raft** (`wal` do etcd, volume próprio) é curto e tem fsync antes do ACK. O **log de replicação** (log segmentado próprio, outro volume, [ADR-055](../03_adr/ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)) é nosso, longo, gravado depois do commit **sem fsync**, com checkpoint com sync periódico, e serve a Azure atrasada, o `drctl` e a reconstrução. O `CommitPump` liga o commit ao gate, ao applier (pela memória) e ao log de replicação. O **gate é alimentado por evento**: cada resposta de `AppendEntries` dos learners Azure (termo, índice, identidade) o atualiza na hora, sem esperar um novo commit. O applier só avança até o gate e persiste o progresso **por item** no volume do log. A escrita do log do Raft passa pelo **escritor de group commit por shard** ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)): junta o que chegou durante o fsync anterior num único `Save` com `fdatasync` e só depois libera as respostas; só o `MsgApp` da líder sai antes.

## Caminho do dado em um nó

```mermaid
flowchart TD
    W["WriteService: Propose"] --> RAFT["RaftNode"]
    RAFT -.->|"buffer cheio: 32 MiB por shard"| THR["ThrottlingException, nada entra no log"]
    RAFT -->|"lote do Ready"| WR["raftstore.Writer<br/>junta os pendentes, teto 1 MiB, sem espera fixa"]
    WR -->|"um Save, um fdatasync"| RLOG[("log do Raft: wal do etcd<br/>vida curta, volume do Raft")]
    WR -->|"depois do fsync: MsgAppResp, votos, auto-ACK, Advance"| RAFT
    RAFT -->|"MsgApp da líder antes do fsync, F3"| PARES["Seguidores AWS e learners Azure"]
    RAFT -->|"commit"| PUMP["CommitPump"]
    PARES -->|"respondem só depois do próprio fsync"| AZ["Respostas do AppendEntries<br/>dos learners Azure: termo, índice, identidade"]
    AZ -->|"por evento, só termo atual"| GATE["Gate"]
    PUMP -->|"índice de commit"| GATE
    PUMP -->|"entradas pela memória"| APPL["Applier"]
    PUMP -->|"fila limitada, goroutine própria, sem fsync, uma escrita por lote (ADR-059)"| REP[("log de replicação segmentado<br/>vida longa, volume próprio")]
    GATE -->|"libera ACK"| W
    GATE -->|"libera o apply até o gate"| APPL
    APPL -->|"escrita condicional por versão, paralelo por chave (ADR-058)"| DB[("Banco da célula")]
    APPL -->|"marca d'água: índice e ordem, sem fsync"| REP
    CK["Checkpointer: fsync a cada REPLOG_SYNC_INTERVAL,<br/>fora da trava do Append (ADR-059)"] --> REP
    CK -.->|"compacta até RaftCompactLimit"| RLOG
    REP -.->|"DurableIndex do checkpoint: até onde o log do Raft pode ser truncado"| RLOG
```

## Onde é detalhado

- Desenho: [03_consenso_raft_quorum_e_gate](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.5 e 5.5.1) e [ADR-018](../03_adr/ADR-018-dois-logs-raft-e-replicacao.md).
- Gate por termo e por evento: [03_consenso_raft_quorum_e_gate](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.3.1) e [ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md).
- Retenção, progresso por item e fsync: [04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md) (6.3.1) e [ADR-048](../03_adr/ADR-048-skip-por-item.md).
- Log segmentado, checkpoint e regras de retenção: [04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md) (6.9) e [ADR-055](../03_adr/ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md).
- Group commit, regra de envio e contrapressão: [04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md) (6.10) e [ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md).
- Coerência na partida: [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md).

# D04 — CommitPump e o log do Raft

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

O **log do Raft** (`wal` do etcd, um por shard, num volume de bloco dedicado por nó) é o único log do nó: tem fsync antes do ACK e é a única fonte das entradas commitadas. Ele também retém em disco o que as réplicas atrasadas dentro da janela ainda precisam, até o teto duro do volume ([especificação](../../spec/compactacao_do_log/especificacao.md), seção 4). O `CommitPump` liga o commit ao gate e ao applier (pela memória). O **gate é alimentado por evento**: cada resposta de `AppendEntries` dos learners Azure (termo, índice, identidade) o atualiza na hora, sem esperar um novo commit. O applier só avança até o gate e grava o progresso **por item** (marca d'água: índice e ordem) num registro pequeno com CRC, sem fsync, no volume do Raft. A escrita do log do Raft é o **group commit por shard** do laço do `Ready` ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)): grava o que chegou durante o fsync anterior num único `Save` com `fdatasync` e só depois libera as respostas; só o `MsgApp` da líder sai antes. Pelo [ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md), **o commit é um sinal e não uma fila**: o laço do Raft publica o índice commitado e nunca espera ninguém; o `CommitPump` lê as entradas por faixa pelo `LogReader` (da RAM ou, abaixo dela, dos segmentos `.wal`) quando o applier tem espaço, e o gate segue o commit numa goroutine própria. A líder recusa escrita (`atraso_apply`) quando `gate − aplicado` passa de `APPLY_LAG_MAX`. O **`Compactor`**, fora do laço, grava o snapshot-marcador (sem dados), compacta a RAM até `snapshot − RAFT_CATCHUP_ENTRIES` (ou até o snapshot acima de `RAFT_RAM_MAX`) e libera o disco até `min(aplicado com fsync, piso, pinos, snapshot)`. O piso é calculado pela líder (menor `Match` das réplicas na janela) e vai às followers junto com o gate. Ligado na POC desde o Incremento A (18/10).

## Caminho do dado em um nó

```mermaid
flowchart TD
    W["WriteService: admissão e Propose"] --> RAFT["RaftNode"]
    W -.->|"gate − aplicado &gt; APPLY_LAG_MAX (ADR-060)"| THR
    RAFT -.->|"buffer cheio: 32 MiB por shard"| THR["ThrottlingException, nada entra no log"]
    RAFT -->|"lote do Ready"| WR["Laço do Ready do shard<br/>grava os pendentes, sem espera fixa"]
    WR -->|"um Save, um fdatasync"| RLOG[("log do Raft: wal do etcd por shard<br/>segmentos .wal no volume do Raft")]
    WR -->|"Append: só a cauda desde o snapshot"| RAM[("MemoryStorage<br/>teto RAFT_RAM_MAX")]
    WR -->|"depois do fsync: MsgAppResp, votos, auto-ACK, Advance"| RAFT
    RAFT -->|"MsgApp da líder antes do fsync, F3"| PARES["Seguidores AWS e learners Azure"]
    RAFT -->|"commit publicado como sinal: atômico + aviso, nunca espera (ADR-060)"| SIG["Sinal de commit"]
    SIG --> PUMP["CommitPump"]
    RAM -.->|"cauda recente"| LR["LogReader<br/>só entradas commitadas e definitivas"]
    RLOG -.->|"abaixo da RAM, em modo leitura"| LR
    LR -->|"entradas por faixa"| PUMP
    PARES -->|"respondem só depois do próprio fsync"| AZ["Respostas do AppendEntries<br/>dos learners Azure: termo, índice, identidade"]
    AZ -->|"por evento, só termo atual"| GATE["Gate"]
    PUMP -->|"índice de commit, goroutine própria"| GATE
    PUMP -->|"entradas pela memória, fila de 64 lotes"| APPL["Applier"]
    GATE -->|"libera ACK"| W
    GATE -->|"libera o apply até o gate"| APPL
    APPL -->|"escrita condicional por versão, paralelo por chave (ADR-058)"| DB[("Banco da célula")]
    APPL -->|"marca d'água: índice e ordem, CRC, sem fsync"| PROG[("progresso do applier<br/>arquivo progress no volume do Raft")]
    CMP["Compactor, fora do laço<br/>gatilho: 10.000 entradas ou 64 MiB"] -->|"1. fsync do progresso"| PROG
    CMP -->|"2. marcador: cluster_id, epoch, confirmado"| MARK[("arquivo snapshot<br/>CRC, fsync")]
    CMP -->|"3. wal.SaveSnapshot; 5. ReleaseLockTo e remoção<br/>até min(aplicado com fsync, piso, pinos, snapshot)"| RLOG
    CMP -->|"4. Compact até snapshot − catchup<br/>ou até o snapshot no teto"| RAM
    GATE -.->|"confirmado"| CMP
    CMP -.->|"piso: menor Match na janela, vai no GateUpdate"| PARES
    MARK -.->|"na partida: confirmado restaura o gate"| GATE
    PROG -.->|"na partida: retomada; WAL relido só desde o snapshot"| PUMP
```

## Onde é detalhado

- Desenho: [03_consenso_raft_quorum_e_gate](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.5 e 5.5.1) e [ADR-019](../03_adr/ADR-019-wal-proprio-volume-dedicado.md).
- Gate por termo e por evento: [03_consenso_raft_quorum_e_gate](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.3.1) e [ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md).
- Progresso por item e fsync: [04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md) (6.3.1) e [ADR-048](../03_adr/ADR-048-skip-por-item.md).
- Retenção e compactação do log do Raft: [especificação](../../spec/compactacao_do_log/especificacao.md) e [decisões](../../spec/compactacao_do_log/decisoes.md) de 18/10, [04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md) (6.9); substituem [ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) e [ADR-063](../03_adr/ADR-063-snapshot-e-compactacao-do-log-do-raft.md), a revisar.
- Group commit, regra de envio e contrapressão: [04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md) (6.10) e [ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md).
- Commit como sinal, admissão por atraso do apply e banco lento: [ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md) e [C35](../02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md).
- Partida e recuperação: [X04](../02_cenarios/complexos/X04-partida-e-recuperacao-do-no.md).

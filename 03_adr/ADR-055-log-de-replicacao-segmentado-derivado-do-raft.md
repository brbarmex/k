# ADR-055 — Log de replicação segmentado, derivado do Raft, sem fsync no caminho do ACK e em volume separado

**Status:** Decidido (Bruno, 13/10/2026: respostas 1a 2b 3b; Pebble trocado por log segmentado na resposta "opção a") · **Data:** 13/10/2026 · **Revisa:** [ADR-018](ADR-018-dois-logs-raft-e-replicacao.md), [ADR-019](ADR-019-wal-proprio-volume-dedicado.md); **complementa:** [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), [ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)

## Contexto

O desenho anterior gravava os dois logs ([ADR-018](ADR-018-dois-logs-raft-e-replicacao.md)) com um WAL próprio com fsync, e esse WAL estava no caminho do ACK ([ADR-019](ADR-019-wal-proprio-volume-dedicado.md)). O próprio pacote o registrava como **o maior risco de perda de dado do projeto**.

O Bruno propôs o Pebble para o log de replicação. A discussão levou a três decisões (1a 2b 3b): o log de replicação passa a ser **derivado do Raft e sem fsync**, fica em **volume separado** e o **teto de 70%** é mantido. Com a 1a, porém, o motivo para usar uma biblioteca pronta quase desaparece:

- O log de replicação sai do caminho do ACK e pode ser refeito a partir do Raft. Um bug nele não perde dado confirmado.
- O acesso é de **log**, não de chave-valor: anexar no fim, ler em sequência, apagar o começo. Num LSM, apagar o começo é uma marcação que só libera o disco depois da compactação; num log segmentado, é remover arquivos.
- O Pebble traria memtable e cache (acima da meta de 256 MiB do pod), compactação regravando dados e uma dependência grande.

Por isso o Bruno escolheu (**opção a**) um **log segmentado simples, nosso**, no lugar do Pebble.

## Decisão

1. **Log de replicação = log segmentado próprio, derivado do log do Raft (1a).** Pacote `adapter/out/replog`, um log por shard. Cada entrada commitada é anexada **depois do commit, sem fsync**. O log **não está no caminho do ACK**: a durabilidade que conta para o ACK e para o gate continua sendo o fsync do log do Raft (votantes AWS e learners Azure).
   - Segmentos de tamanho fixo (proposta: 64 MiB), nome = primeiro índice. Registro: `len u32 | crc32c u32 | payload` (post-image, versão, nome lógico, itens com ordem). Nenhum tipo `raftpb` no disco.
   - O progresso do applier por item `(índice, ordem)` ([ADR-048](ADR-048-skip-por-item.md)) fica num registro pequeno com CRC no mesmo diretório, também sem fsync. Perdê-lo numa queda de host só alonga o replay, que é idempotente pela versão.
2. **Checkpoint durável.** Fora do caminho do ACK, a cada `REPLOG_SYNC_INTERVAL` (proposta: 100 ms ou 8 MiB, o que vier antes), um `fsync` do segmento ativo. O maior índice coberto por esse fsync é o **`DurableIndex`**. Ao rotacionar, o segmento que sai é sincronizado e o diretório também; só o segmento ativo pode ter dados sem fsync.
3. **Invariante de truncamento (segurança):** o log do Raft **só é compactado até o `DurableIndex`** do log de replicação, **nunca até o índice apenas copiado**. Uma cópia sem fsync some numa queda de host; se o Raft já tivesse sido truncado, a entrada não existiria em lugar nenhum daquele nó. Implementado em `domain.RaftCompactLimit`.
4. **Retenção para seguidores e learners (desempenho, não segurança):** o log do Raft também guarda uma folga até o menor índice confirmado pelos pares, limitada por `RAFT_RETAIN_MAX` (proposta: 64 MiB ou 30 s de escrita). Quem passar dessa folga faz catch-up pelo log de replicação ([C13](../02_cenarios/atualizados/C13-ressincronizacao-da-azure.md)). **A retenção para a Azure atrasada no modo degradado fica no log de replicação, não no Raft**: o log do Raft continua curto ([C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)). O log de replicação só é truncado até `min(applied local, índice durável da maioria Azure, F − 1 de um rebuild em curso)` (`domain.ReplogTruncateLimit`), respeitado o teto do [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) e o tempo mínimo que cobre a janela de dedupe. O truncamento apaga **segmentos inteiros**.
5. **Catch-up exige fsync no learner.** Quando um learner recebe entradas pelo canal de catch-up (fora do `AppendEntries`), ele grava no próprio log de replicação e faz **fsync** antes de informar ao Raft o índice alcançado. Nesse caminho o log de replicação do learner é a única cópia durável daquelas entradas no nó, e sem o fsync o gate contaria uma durabilidade da Azure que não existe.
6. **Recuperação:** na abertura, a cauda rasgada do último segmento é truncada; registro inválido em qualquer outro lugar é corrupção e o nó não sobe. A abertura faz um fsync e só então publica o `DurableIndex`. Depois o log é completado a partir do Raft; entradas que já existem são ignoradas (a chave é o índice).
7. **Log do Raft:** `etcd-io/raft` com o pacote `wal` do etcd (`go.etcd.io/etcd/server/v3/storage/wal`, W1 da seção 6.2), um WAL por shard, fsync antes de responder, em volume próprio (pergunta 16a). Group commit por shard, regra "nada sai antes do fsync", modo síncrono com F3 e critério do `AsyncStorageWrites`: [ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md).
8. **Volume separado (2b).** Cada nó, nas **duas nuvens** (3 votantes AWS e 3 learners Azure por instalação), tem **dois volumes**: um pequeno e rápido para o log do Raft (fsync no caminho do ACK) e um maior para o log de replicação (fsync só no checkpoint).
9. **Teto de 70% mantido como está (3b).** Vale para o volume do log de replicação. Como o truncamento apaga segmentos inteiros, o espaço **lógico** que o teto mede e o espaço **físico** ocupado diferem só pelo segmento parcialmente necessário (até um segmento por shard). O risco de o físico ficar muito acima do lógico, que existia com o Pebble, deixa de existir.
10. **Erro de escrita ou de fsync no log de replicação é fatal para o processo**, como o fsync do Raft ([ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)). O nó reinicia e refaz o trecho perdido a partir do Raft. **Perda só do volume do log de replicação** é tratada como disco perdido: o nó volta com identidade nova (padrão adotado; pergunta 17 em aberto).

## Alternativas consideradas

- **Pebble, derivado do Raft e sem fsync** (a primeira versão deste ADR): leitura por faixa, `DeleteRange` e recuperação prontos. Descartado pelo Bruno: o padrão de acesso é de log, o disco só volta depois da compactação, e memtable, cache e compactação custam memória e escrita sem necessidade de acesso por chave.
- **Log de replicação durável por conta própria, com fsync antes do ACK (1b):** dois fsyncs no caminho do ACK. Descartado.
- **Mesmo volume do Raft (2a):** mais barato, mas o checkpoint e as leituras de catch-up disputariam o dispositivo do fsync do Raft. Descartado em favor do isolamento.
- **Teto medido no espaço real com compactação forçada (3a):** fazia sentido para um LSM; com segmentos, lógico e físico quase coincidem.
- **WAL próprio para os dois logs no caminho do ACK (W2, desenho anterior):** substituído.

## Consequências

- **O caminho do ACK tem um único fsync por nó**, o do Raft.
- **O código de armazenamento próprio continua, mas fora do caminho do ACK.** Um bug no `replog` não perde dado confirmado: o log é refeito a partir do Raft enquanto a invariante 3 valer. Por isso o teste mais importante é o da invariante, com queda de host simulada.
- **Durabilidade em degraus.** Entre dois checkpoints as entradas existem só no log do Raft daquele nó e no cache de páginas. Isso não muda o RPO: a entrada confirmada está no log do Raft com fsync em 2 de 3 da AWS e 2 de 3 da Azure.
- **W1 (WAL do etcd):** um fsync **por shard**, com group commit dentro do shard e sem group commit entre shards ([ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)); o custo é IOPS, aceitável com poucos shards (premissa A3) e IOPS provisionadas, a medir no spike S1; o `raft.MemoryStorage` mantém em memória as entradas não compactadas, mais um motivo para a retenção longa ficar fora do Raft; a dependência `go.etcd.io/etcd/server/v3` é pesada (traz `zap` e `bbolt`), isolada no adaptador `etcdraft`.
- **Memória:** o log segmentado só guarda em memória a lista de segmentos e um buffer; sem cache de blocos.
- **Leitura por faixa sem índice esparso** no esboço: o `Read` varre os segmentos que cobrem a faixa. Para o catch-up é suficiente; um índice esparso por segmento entra se a medição pedir.
- **Custo:** um volume a mais por nó, 6 por instalação, e cada cliente tem a sua instalação ([ADR-043](ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md)). O volume do Raft fica pequeno; o do log de replicação pode ser de classe mais barata. Estimativa em [R20](../02_cenarios/a_rever/R20-custo-6x.md).
- **Kubernetes:** dois `volumeClaimTemplates` por StatefulSet, os dois na AZ do nó.
- **Riscos:** 29, 31 e 32 em [02_riscos](../06_pendencias_e_riscos/02_riscos.md) (o 30 foi encerrado com a troca); pendência PEND-22.

## Código de exemplo

- `internal/adapter/out/replog`: o log segmentado, com testes de cauda rasgada, corrupção, rotação, compactação por segmento e `DurableIndex` só depois do `Sync`.
- `internal/domain/retention.go`: `RaftCompactLimit` e `ReplogTruncateLimit`.
- `internal/usecase/apply.go` (applier serial até o gate, progresso por item) e `internal/usecase/checkpoint.go` (sync, compactação do Raft e truncamento do log de replicação).

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.5.1)
- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md) (6.0 e 6.9)
- [X04-partida-recuperacao-e-dois-logs.md](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md)
- [C13-ressincronizacao-da-azure.md](../02_cenarios/atualizados/C13-ressincronizacao-da-azure.md)
- [C29-disco-do-log-em-degradacao-longa.md](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)
- [D04-dois-logs-e-commitpump.md](../04_diagramas/D04-dois-logs-e-commitpump.md)

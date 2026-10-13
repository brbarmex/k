# ADR-055 — Log de replicação em Pebble, derivado do Raft, sem fsync no caminho do ACK e em volume separado

**Status:** Decidido (Bruno, 13/10/2026: respostas 1a 2b 3b) · **Data:** 13/10/2026 · **Revisa:** [ADR-018](ADR-018-dois-logs-raft-e-replicacao.md), [ADR-019](ADR-019-wal-proprio-volume-dedicado.md); **complementa:** [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), [ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)

## Contexto

O desenho anterior gravava os dois logs ([ADR-018](ADR-018-dois-logs-raft-e-replicacao.md)) com um motor de arquivo segmentado próprio (W2, [ADR-019](ADR-019-wal-proprio-volume-dedicado.md)). O próprio pacote registrava esse WAL como **o maior risco de perda de dado do projeto** (bug de armazenamento). O Bruno propôs usar o **Pebble** (`cockroachdb/pebble`, LSM em Go puro, sem CGO) para o log de replicação e manter o Raft no `etcd-io/raft`.

O log de replicação é lido **por faixa** (catch-up da Azure em [C13](../02_cenarios/atualizados/C13-ressincronizacao-da-azure.md), `drctl`, reconstrução desde F em [C33](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md)) e truncado por prefixo. Com chaves ordenadas por `(shard, índice)`, um iterador e um `DeleteRange` resolvem isso. Os custos de um LSM (amplificação de escrita, compactação disputando disco, espaço liberado só depois da compactação, memória de memtable e cache) foram discutidos antes da decisão.

## Decisão

1. **Log de replicação no Pebble, derivado do log do Raft (1a).** Cada entrada commitada é copiada para o Pebble **depois do commit**, com `pebble.NoSync`. O Pebble **não está no caminho do ACK**: a durabilidade que conta para o ACK e para o gate continua sendo o fsync do log do Raft (nos votantes AWS e nos learners Azure).
   - Chave: `r/{shard u32 big-endian}/{índice u64 big-endian}`; valor: o registro DataReplication (post-image, versão, nome lógico, itens com ordem), com CRC32C próprio. Nenhum tipo `raftpb` entra no Pebble.
   - O progresso do applier por item `(índice, ordem)` ([ADR-048](ADR-048-skip-por-item.md)) e o high-water mark ficam no mesmo Pebble, em outro prefixo (`p/{shard}`), também com `NoSync`. Como o Pebble recupera um **prefixo** das escritas na ordem em que foram feitas, progresso e truncamento continuam coerentes entre si depois de uma queda.
2. **Checkpoint durável.** Fora do caminho do ACK, a cada `PEBBLE_SYNC_INTERVAL` (proposta: 100 ms ou 8 MiB, o que vier antes, a medir), o nó faz uma gravação com `pebble.Sync`. Ela torna durável tudo o que veio antes no WAL do Pebble. O maior índice coberto por esse sync é o **`DurableIndex`** do log de replicação.
3. **Invariante de truncamento (segurança):** o log do Raft **só é compactado até o `DurableIndex`** do Pebble (o índice coberto por um sync), **nunca até o índice apenas copiado**. Uma cópia sem sync some numa queda de host; se o Raft já tivesse sido truncado, a entrada não existiria em lugar nenhum daquele nó.
4. **Retenção para seguidores e learners (desempenho, não segurança):** o log do Raft também guarda uma folga até o menor índice confirmado pelos seguidores e learners, limitada por `RAFT_RETAIN_MAX` (proposta: 64 MiB ou 30 s de escrita, a medir). Quem passar dessa folga sai do caminho normal do Raft e faz catch-up pelo Pebble ([C13](../02_cenarios/atualizados/C13-ressincronizacao-da-azure.md)). **A retenção para a Azure atrasada no modo degradado fica no Pebble, não no Raft**: o log do Raft continua curto ([C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)). O Pebble só trunca até `min(applied local, índice durável da maioria Azure, F de um rebuild em curso)`, respeitado o teto do [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) e o tempo mínimo que cobre a janela de dedupe.
5. **Catch-up pelo Pebble exige sync no learner.** Quando um learner recebe entradas pelo canal de catch-up (fora do `AppendEntries`), ele grava no próprio Pebble **com `pebble.Sync`** antes de informar ao Raft o índice alcançado. Nesse caminho o Pebble do learner é a única cópia durável daquelas entradas no nó, e o gate passaria a contar uma durabilidade que não existe.
6. **Recuperação:** na partida, o nó abre o Pebble, lê o maior índice presente e completa o log de replicação a partir do log do Raft antes de aceitar tráfego. Entradas acima do `DurableIndex` que sobreviveram são regravadas sem efeito (a chave é o índice).
7. **Log do Raft:** `etcd-io/raft` com o pacote `wal` do etcd (`go.etcd.io/etcd/server/v3/storage/wal`, W1 da seção 6.2), um WAL por shard, fsync antes de responder, em volume próprio. Ver as consequências abaixo e a pergunta aberta na [lista de perguntas](../06_pendencias_e_riscos/03_perguntas_para_o_bruno.md).
8. **Volume separado (2b).** Cada nó, nas **duas nuvens** (3 votantes AWS e 3 learners Azure por instalação), tem **dois volumes**: um pequeno e rápido para o log do Raft (fsync no caminho do ACK) e um maior para o Pebble (compactação fora do caminho do ACK). A compactação do Pebble não disputa o fsync do Raft.
9. **Teto de 70% mantido como está (3b).** O teto do [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) passa a valer para o volume do Pebble e mede o **espaço lógico retido** (do primeiro índice retido até a cabeça). O risco de o espaço físico ser maior está registrado (risco 30).
10. **Erro de escrita ou de sync no Pebble é fatal para o processo**, como o fsync do Raft ([ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)). O nó reinicia e reconstrói o trecho perdido a partir do Raft. **Perda só do volume do Pebble** é tratada como disco perdido: o nó volta com identidade nova (padrão adotado; ver a pergunta aberta).

## Alternativas consideradas

- **Pebble durável por conta própria, com fsync antes do ACK (1b):** dois fsyncs no caminho do ACK e a cauda do segundo somada à do primeiro. Descartado.
- **Mesmo volume com limite de banda para a compactação (2a):** mais barato, mas a compactação e o fsync do Raft disputariam o mesmo dispositivo; seria preciso medir e ajustar o limite. Descartado pelo Bruno em favor do isolamento.
- **Teto medido no espaço real em disco, com compactação forçada acima de 60% (3a):** mais previsível na degradação longa. Não adotado na v1.
- **WAL segmentado próprio para os dois logs (W2, desenho anterior):** um fsync servia vários shards, mas o código de armazenamento era o maior risco de perda de dado.

## Consequências

- **O caminho do ACK fica com um único fsync por nó** (o do Raft), como antes. Os problemas 1 (dois fsyncs) e 2 (compactação disputando o disco do Raft) da discussão deixam de existir.
- **O código de armazenamento próprio sai do projeto.** O risco passa a ser o de configuração e de uso correto de duas bibliotecas maduras.
- **Durabilidade do log de replicação passa a ser em degraus.** Entre dois checkpoints, as entradas existem só no Raft daquele nó e no cache de páginas do kernel. Isso não muda o RPO: a entrada ACKada está no log do Raft com fsync em 2/3 da AWS e 2/3 da Azure, e a invariante 3 impede truncar o Raft antes do checkpoint.
- **W1 (WAL do etcd) muda três coisas:** (a) um fsync **por shard** e sem group commit entre shards, aceitável com poucos shards (premissa A3), a medir no spike S1 com k shards em paralelo; (b) o `raft.MemoryStorage` mantém as entradas não compactadas **em memória**, o que é mais um motivo para a retenção longa ficar no Pebble e não no Raft; (c) a dependência `go.etcd.io/etcd/server/v3` é pesada (traz `zap`, `bbolt` e outras) e o `wal` recebe um `*zap.Logger`. O adaptador `etcdraft` isola isso, e o peso entra na medição do binário e da memória.
- **Memória do Pebble entra no orçamento do pod:** memtable (proposta: 2 × 32 MiB) e cache de blocos (proposta: 64 a 128 MiB). Isso compete com a meta de 256 MiB RSS ([07_go_memoria_cpu_e_disco](../01_arquitetura/07_go_memoria_cpu_e_disco.md)), que precisa ser revista no spike S2.
- **Custo:** um volume a mais por nó, 6 por instalação, e a solução é uma instalação por cliente ([ADR-043](ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md)). Em compensação, o volume do Raft fica pequeno (guarda segundos de escrita) e só ele precisa de IOPS provisionado para fsync; o do Pebble é grande e pode ser de uma classe mais barata. A estimativa entra em [R20](../02_cenarios/a_rever/R20-custo-6x.md).
- **Kubernetes:** dois `volumeClaimTemplates` por StatefulSet, os dois presos à mesma AZ do nó.
- **Riscos novos:** 29 a 32 em [02_riscos](../06_pendencias_e_riscos/02_riscos.md); pendência PEND-22.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.5.1)
- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md) (6.0 e 6.9)
- [X04-partida-recuperacao-e-dois-logs.md](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md)
- [C13-ressincronizacao-da-azure.md](../02_cenarios/atualizados/C13-ressincronizacao-da-azure.md)
- [C29-disco-do-log-em-degradacao-longa.md](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)
- [D04-dois-logs-e-commitpump.md](../04_diagramas/D04-dois-logs-e-commitpump.md)

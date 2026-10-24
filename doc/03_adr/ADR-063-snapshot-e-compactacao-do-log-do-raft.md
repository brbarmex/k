# ADR-063 — Snapshot e compactação do log do Raft

**Status:** **Substituído pelo ADR-065** (24/10/2026); era Pendente · **Data:** 14/10/2026 · **Origem:** revisão geral do projeto de 14/10 · **Fecha (quando decidido):** RT-11 e o risco 7 da spec da POC · **Precisa:** [ADR-019](ADR-019-wal-proprio-volume-dedicado.md), [ADR-060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md), [ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md), [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) · **Afeta o estado atual:** seções 3.5 e 3.11, quando decidido · **Relacionado:** [ADR-062](ADR-062-banco-com-versao-de-outra-historia-do-log.md), [ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)

## Contexto

### O que a arquitetura já define

- O "snapshot" do Raft aqui é só um **marcador** (índice, termo, configuração), porque o estado vive no banco da célula e não no `DataReplication` ([04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md), 6.6).
- O log do Raft é o único log do nó e a única fonte das entradas commitadas ([ADR-019](ADR-019-wal-proprio-volume-dedicado.md)). Ele só é compactado até `domain.RaftCompactLimit = min(aplicado local, índice durável da maioria Azure enquanto a Azure estiver dentro da janela, F − 1 de uma reconstrução em tabela sombra em curso)` (regra de segurança), com uma folga até o menor `match` dos votantes AWS, limitada por `RAFT_RETAIN_MAX` (regra de desempenho). O aplicado local já está no banco da célula, então compactar até ele é seguro.
- A retenção para a Azure atrasada fica no próprio log do Raft, com o teto de 70% do volume do Raft ([ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)). Acima do teto a Azure sai da janela e deixa de segurar a compactação.
- Uma réplica atrasada além do que o log do Raft retém se reconstrói em tabela sombra ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).

### O que o código faz hoje (POC, RT-11)

- A compactação está desligada: `Node.Compact` não faz nada (`internal/adapter/out/etcdraft/node.go`) e não há laço de compactação.
- O `MemoryStorage` guarda o log inteiro, e a memória do processo cresce sem limite (risco 7 da spec).
- O WAL nunca é liberado (`wal.ReleaseLockTo`), e a partida relê o WAL inteiro desde o snapshot sintético do RT-03.
- Receber um `MsgSnap` é erro fatal; enviar vira `ReportSnapshot(SnapshotFailure)`. Um par que precise de snapshot nunca alcança o grupo.

### O que ainda não está decidido

1. **A mecânica** no `etcd-io/raft` e no `wal` do etcd: quando gerar o snapshot, `MemoryStorage.CreateSnapshot` + `Compact`, `wal.SaveSnapshot`, `wal.ReleaseLockTo` e a remoção dos arquivos antigos do WAL.
2. **O que o snapshot precisa carregar.** Hoje há estado que só existe como entrada do log e se perde quando o prefixo for compactado:
   - o modo degradado: `DEGRADE`/`RESTORE`, `epoch` e validade do lease. `DegradeService.Adopt` (`internal/usecase/degrade.go`) recupera o modo **reencontrando essas entradas no log**. Depois de uma compactação, uma líder nova ou um nó reiniciado não as encontra e volta ao modo normal sem saber que estava degradado ([ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)). Na POC o `Adopt` ainda não tem chamador, então o problema não aparece nela.
   - os `SKIP` de itens venenosos ([ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md), [ADR-048](ADR-048-skip-por-item.md));
   - a janela de dedupe ([ADR-026](ADR-026-dedupe-de-retry-id-estavel.md), [R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md));
   - o estado do gate na troca de líder (D9);
   - o `cluster_id` e a `epoch` da história ([ADR-062](ADR-062-banco-com-versao-de-outra-historia-do-log.md));
   - a `ConfState`, que o próprio Raft já carrega.
3. **O par atrás da compactação.** Quando um votante precisa de entradas já compactadas, o Raft da líder manda um `MsgSnap`. Como o snapshot é só um marcador, **aceitá-lo ingenuamente avança o índice do Raft da follower sem os itens no banco dela**: divergência silenciosa, da mesma família do ADR-062. O caminho seguro precisa trazer os dados antes de dar o índice por aplicado, e as entradas compactadas não estão retidas em lugar nenhum.
4. **A interação com a partida do ADR-060.** O ponto de retomada é o índice do progresso do applier, e o `Applied` do Raft é limitado por baixo pelo índice do snapshot (`restartApplied`). O progresso é gravado sem fsync; com compactação, a retomada pode ficar **antes** do snapshot, e as entradas entre a retomada e o snapshot já não estão no WAL nem no `MemoryStorage`. Esse caso precisa de regra e de teste.

## Proposta

1. **Gatilho e mecânica.**
   - Um laço de compactação por shard, a cada ciclo, calcula o limite pela regra `domain.RaftCompactLimit` (aplicado local, índice durável da maioria Azure enquanto ela estiver na janela, F − 1 de uma reconstrução em curso), mais a folga até o menor `match` dos votantes AWS limitada por `RAFT_RETAIN_MAX`.
   - Quando o limite passa do último snapshot por pelo menos `SNAPSHOT_EVERY` entradas (proposta: 10.000, como o padrão do etcd, ou 64 MiB), o laço grava o progresso do applier com fsync e grava o snapshot no WAL (`wal.SaveSnapshot`), cria o snapshot no `MemoryStorage` (`CreateSnapshot`), compacta (`Compact`) e libera os arquivos antigos do WAL (`ReleaseLockTo` e remoção).
   - Nunca além do que o applier gravou no banco, regra que o primeiro termo do limite (aplicado local) já garante ([ADR-060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)).
2. **Conteúdo do snapshot (`Data`).** Um registro versionado e com CRC, com:
   - `cluster_id` e `epoch`;
   - o modo (normal ou degradado), com a epoch e a validade do último `DEGRADE`/`RENEW`;
   - os `SKIP` em vigor;
   - o que for preciso para reconstruir a janela de dedupe e o gate (D9).
   O `Adopt` passa a partir do snapshot e depois segue pelas entradas posteriores a ele.
3. **Par atrás da compactação: marcador, applier cercado e reconstrução.**
   - A follower aceita o `MsgSnap` **no Raft** (o índice e a configuração avançam), mas o applier dela fica **cercado**: não marca nada como aplicado além do que de fato gravou.
   - Como as entradas entre `aplicado + 1` e o índice do snapshot já foram compactadas e não estão retidas em lugar nenhum, a follower volta por **reconstrução em tabela sombra** ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)), com F no índice do snapshot. Só depois do cutover o applier volta ao normal, seguindo as entradas posteriores ao snapshot.
   - Enquanto isso, a leitura forte nessa follower espera o apply, como hoje: com o apply atrás da barreira, ela não responde dado velho.
4. **Partida.** Quando a retomada fica antes do snapshot, as entradas que faltam não existem mais no nó: a célula não sobe e exige reconstrução. O fsync do progresso antes de cada snapshot (item 1) faz com que uma queda de host comum não caia nesse caso; ele fica para o progresso perdido ou corrompido.

## Decisões em aberto

1. **Gatilho:** (a) por número de entradas (`SNAPSHOT_EVERY`, proposta 10.000); (b) por bytes retidos no `MemoryStorage` (proposta 64 MiB); (c) os dois, o que vier primeiro. Recomendação: **c**.
2. **Conteúdo do snapshot:** (a) todo o estado derivado do log listado acima, num registro versionado; (b) só `ConfState`, e o resto em arquivos próprios no volume do Raft. Recomendação: **a**, um único ponto de verdade que viaja com o `MsgSnap`.
3. **Par atrás da compactação:** (a) marcador no Raft com applier cercado e reconstrução em tabela sombra; (b) nunca enviar `MsgSnap`: o par fica fora do Raft até reconstrução manual (o comportamento da POC, mas em produção); (c) tirar o par do grupo e readmiti-lo com identidade nova depois da reconstrução, como no disco perdido ([ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)). Recomendação: **a**. O (b) deixa o par sem receber nada do Raft enquanto a reconstrução não termina, e o (c) mexe na configuração do grupo num caso em que o log do par está íntegro.
4. **Escopo na POC:** (a) implementar 1, 2 e 4 na POC e provar 3 com um teste de unidade e um cenário de follower parada além da compactação; (b) implementar tudo na POC; (c) só desenho, implementação na fase AWS. Recomendação: **a**. Limita a memória da POC e prova o caminho perigoso sem construir a reconstrução completa.
5. **`RAFT_RETAIN_MAX`:** (a) manter a proposta de 64 MiB ou 30 s; (b) medir no spike S1 antes de fixar. Recomendação: **b**.

## Alternativas consideradas

- **Não compactar nunca e reiniciar o processo periodicamente:** a partida relê o WAL inteiro, cada vez mais devagar, e a memória cresce entre reinícios. Descartado para produção.
- **Snapshot com a cópia do banco (o snapshot clássico do Raft):** o estado é o DynamoDB inteiro da célula; copiá-lo pelo Raft é inviável. A cópia de estado já existe, e é a reconstrução em tabela sombra.
- **Compactar só até o aplicado local, sem segurar a Azure:** a Azure atrasada perderia o catch-up pelo log a cada compactação e cairia em reconstrução mesmo dentro da janela do [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md). Descartado.

## Consequências (se aprovado como recomendado)

- A memória do Raft e o disco do WAL ficam limitados; a partida relê só desde o último snapshot.
- Um votante ou learner atrasado volta sozinho pelo Raft (`MsgApp`) enquanto o log do Raft retiver o que ele precisa; além disso, volta por reconstrução, nunca com o índice à frente do banco. O `RAFT_RETAIN_MAX` (decisão 5) define quanto atraso de um votante AWS se resolve sem reconstrução.
- O formato do snapshot vira contrato: versão do registro e compatibilidade entre versões do binário na atualização contínua.
- O `Adopt` e o restante do estado derivado do log passam a ter dois caminhos (snapshot e entradas), ambos testados.
- O teste de "follower parada além da compactação" entra nos critérios de aceite da POC.

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

Substituído por [ADR-065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md) (snapshot-marcador, RAM com teto, disco com janela e teto duro, partida pela cauda) e [ADR-066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md) (recuperação e reconstrução). As decisões 1 a 5 deste ADR ficaram sem resposta porque o Bruno pediu o redesenho do zero em 18/10. Implementado na POC (Incremento A, `2ba474d` e `38ae207`).

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md), seções 6.5, 6.6 e 6.9 (regra de compactação e retenção)
- [SPEC_POC_LOCAL.md](../../spec/SPEC_POC_LOCAL.md): RT-03, RT-09, RT-11 e risco 7
- `DataReplicationApp/internal/adapter/out/etcdraft/node.go` (`Compact`, `MsgSnap`), `internal/domain/retention.go`, `internal/usecase/degrade.go` (`Adopt`)

# ADR-063 — Snapshot e compactação do log do Raft

**Status:** Pendente (aguarda o Bruno: decisões 1 a 5) · **Data:** 14/10/2026 · **Origem:** revisão geral do projeto de 14/10 · **Fecha (quando decidido):** RT-11 e o risco 7 da spec da POC · **Precisa:** [ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md), [ADR-059](ADR-059-checkpoint-do-log-de-replicacao-sem-trava-no-append.md), [ADR-060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md), [ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md), [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) · **Afeta o estado atual:** seções 3.5 e 3.11, quando decidido · **Relacionado:** [ADR-062](ADR-062-banco-com-versao-de-outra-historia-do-log.md), [ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)

## Contexto

### O que a arquitetura já define

- O "snapshot" do Raft aqui é só um **marcador** (índice, termo, configuração), porque o estado vive no banco da célula e não no `DataReplication` ([04_wal_e_fsync](../01_arquitetura/04_wal_e_fsync.md), 6.6).
- O log do Raft só é compactado até o `DurableIndex` do log de replicação ([ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md), regra de segurança), com uma folga até o menor índice confirmado pelos pares, limitada por `RAFT_RETAIN_MAX` (regra de desempenho, `domain.RaftCompactLimit`).
- Uma réplica atrasada além do que o log de replicação retém se reconstrói em tabela sombra ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).

### O que o código faz hoje (POC, RT-11)

- `Node.Compact` não faz nada e `MinPeerMatch` devolve 0 (`internal/adapter/out/etcdraft/node.go`). O `Checkpointer` chama a compactação, mas ela não acontece.
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
3. **O par atrás da compactação.** Quando um votante precisa de entradas já compactadas, o Raft da líder manda um `MsgSnap`. Como o snapshot é só um marcador, **aceitá-lo ingenuamente avança o índice do Raft da follower sem os itens no banco dela**: divergência silenciosa, da mesma família do ADR-062. O caminho seguro precisa trazer os dados antes de dar o índice por aplicado.
4. **A interação com a partida do ADR-060.** O ponto de retomada é `min(último do log de replicação + 1, progresso do applier)`, e o `Applied` do Raft é limitado por baixo pelo índice do snapshot (`restartApplied`). Com compactação, a retomada pode ficar **antes** do snapshot: as entradas entre a retomada e o snapshot já não estão no `MemoryStorage` e têm de vir do log de replicação. O caminho existe para o catch-up, mas precisa ser garantido e testado para a partida.

## Proposta

1. **Gatilho e mecânica.**
   - O `Checkpointer`, a cada ciclo, calcula `limite = RaftCompactLimit(DurableIndex, menor match dos pares, RAFT_RETAIN_MAX)`.
   - Quando o limite passa do último snapshot por pelo menos `SNAPSHOT_EVERY` entradas (proposta: 10.000, como o padrão do etcd, ou 64 MiB), ele grava o snapshot no WAL (`wal.SaveSnapshot`), cria o snapshot no `MemoryStorage` (`CreateSnapshot`), compacta (`Compact`) e libera os arquivos antigos do WAL (`ReleaseLockTo` e remoção).
   - Nunca além do que o `CommitPump` entregou, regra que o `DurableIndex` já garante ([ADR-060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)).
2. **Conteúdo do snapshot (`Data`).** Um registro versionado e com CRC, com:
   - `cluster_id` e `epoch`;
   - o modo (normal ou degradado), com a epoch e a validade do último `DEGRADE`/`RENEW`;
   - os `SKIP` em vigor;
   - o que for preciso para reconstruir a janela de dedupe e o gate (D9).
   O `Adopt` passa a partir do snapshot e depois segue pelas entradas posteriores a ele.
3. **Par atrás da compactação: marcador mais catch-up de dados pelo log de replicação.**
   - A follower aceita o `MsgSnap` **no Raft** (o índice e a configuração avançam), mas o applier dela fica **cercado**: não marca nada como aplicado além do que de fato gravou.
   - Ela busca na líder as entradas que faltam, de `aplicado + 1` até o índice do snapshot, pelo mesmo canal de catch-up dos learners ([C13](../02_cenarios/atualizados/C13-ressincronizacao-da-azure.md)), grava no próprio log de replicação e aplica. Só então o applier volta ao normal.
   - Se a líder já não retém essas entradas no log de replicação, a follower exige **reconstrução em tabela sombra** ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)) antes de voltar.
   - Enquanto isso, a leitura forte nessa follower espera o apply, como hoje: com o apply atrás da barreira, ela não responde dado velho.
4. **Partida.** Quando a retomada fica antes do snapshot, o `CommitPump` lê do log de replicação até o snapshot e depois do `MemoryStorage`. Sem as entradas no log de replicação, a célula não sobe e exige reconstrução.

## Decisões em aberto

1. **Gatilho:** (a) por número de entradas (`SNAPSHOT_EVERY`, proposta 10.000); (b) por bytes retidos no `MemoryStorage` (proposta 64 MiB); (c) os dois, o que vier primeiro. Recomendação: **c**.
2. **Conteúdo do snapshot:** (a) todo o estado derivado do log listado acima, num registro versionado; (b) só `ConfState`, e o resto em arquivos próprios no volume do log de replicação. Recomendação: **a**, um único ponto de verdade que viaja com o `MsgSnap`.
3. **Par atrás da compactação:** (a) marcador no Raft com applier cercado e catch-up de dados pelo log de replicação, com reconstrução como último recurso; (b) nunca enviar `MsgSnap`: o par fica fora até reconstrução manual (o comportamento da POC, mas em produção); (c) reconstrução em tabela sombra automática a cada `MsgSnap`. Recomendação: **a**. O (b) transforma qualquer atraso longo num incidente humano, e o (c) custa uma varredura completa onde o log bastaria.
4. **Escopo na POC:** (a) implementar 1, 2 e 4 na POC e provar 3 com um teste de unidade e um cenário de follower parada além da compactação; (b) implementar tudo na POC; (c) só desenho, implementação na fase AWS. Recomendação: **a**. Limita a memória da POC e prova o caminho perigoso sem construir a reconstrução completa.
5. **`RAFT_RETAIN_MAX`:** (a) manter a proposta de 64 MiB ou 30 s; (b) medir no spike S1 antes de fixar. Recomendação: **b**.

## Alternativas consideradas

- **Não compactar nunca e reiniciar o processo periodicamente:** a partida relê o WAL inteiro, cada vez mais devagar, e a memória cresce entre reinícios. Descartado para produção.
- **Snapshot com a cópia do banco (o snapshot clássico do Raft):** o estado é o DynamoDB inteiro da célula; copiá-lo pelo Raft é inviável. A cópia de estado já existe, e é a reconstrução em tabela sombra.
- **Compactar até o índice aplicado, e não até o `DurableIndex`:** viola a regra de segurança do ADR-055 (uma cópia sem fsync some numa queda de host). Descartado.

## Consequências (se aprovado como recomendado)

- A memória do Raft e o disco do WAL ficam limitados; a partida relê só desde o último snapshot.
- Um votante atrasado volta sozinho enquanto o log de replicação reter o que ele precisa; além disso, volta por reconstrução, nunca com o índice à frente do banco.
- O formato do snapshot vira contrato: versão do registro e compatibilidade entre versões do binário na atualização contínua.
- O `Adopt` e o restante do estado derivado do log passam a ter dois caminhos (snapshot e entradas), ambos testados.
- O teste de "follower parada além da compactação" entra nos critérios de aceite da POC.

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md), seções 6.5, 6.6 e 6.9 (três regras de retenção)
- [SPEC_POC_LOCAL.md](../../spec/SPEC_POC_LOCAL.md): RT-03, RT-09, RT-11 e risco 7
- `DataReplicationApp/internal/adapter/out/etcdraft/node.go` (`Compact`, `MinPeerMatch`, `MsgSnap`), `internal/usecase/checkpoint.go`, `internal/domain/retention.go`, `internal/usecase/degrade.go` (`Adopt`)

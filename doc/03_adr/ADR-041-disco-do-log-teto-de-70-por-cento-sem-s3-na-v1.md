# ADR-041 — Disco do log em degradação longa: teto de 70% e reconstrução completa; sem S3 na v1

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

No modo degradado a AWS retém todas as entradas desde `fromIndex` para a Azure fazer catch-up. A 3.000 TPS e cerca de 2 KB por entrada são ~0,5 TB por dia por shard. Sem disco, o fsync falha e não há mais commit.

## Decisão

1. A retenção para a Azure tem **teto de 70% do volume**. Passado o teto, a Azure vira "fora da janela" e voltará por **reconstrução completa** (`drctl rebuild`).
2. **Não há descarga para S3 na v1.**
3. Alerta de ocupação por votante com projeção de tempo até encher.
4. A retenção para a Azure fica **no próprio log do Raft** (disco do WAL e memória do `MemoryStorage`). O teto vale para o **volume do Raft** e é medido pelo **espaço retido** (do primeiro índice retido até a cabeça). Dentro da janela, o índice durável da maioria Azure segura a compactação (`RaftCompactLimit`); acima do teto, a Azure sai da janela, deixa de segurar a compactação e volta por **reconstrução em tabela sombra** ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).

## Alternativas consideradas

- **Descarregar segmentos antigos para o S3:** mantém a Azure elegível por mais tempo, mas o catch-up passa a ler do S3 pelo link, exige credencial AWS na Azure e piora o desastre em que a AWS some.

## Consequências

- Prefere-se perder a elegibilidade da Azure a derrubar o disco da AWS.
- Passado o teto, a Azure não tem como alcançar a AWS pelo log; se a AWS cair nessa janela, a perda é tudo o que foi escrito desde `fromIndex` (órfãs, [R13](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md)). Por isso o tempo máximo de degradação aceito pelo negócio (PEND-02) dimensiona o volume.
- **Memória.** O `MemoryStorage` do etcd-raft guarda em RAM tudo o que não foi compactado; num modo degradado longo a memória do nó cresce com o atraso da Azure até o teto. Mitigações em aberto: teto da janela também por bytes retidos em memória, ou um `raft.Storage` que lê as entradas antigas do WAL em disco. Medir no spike S1.
- O custo e o tempo da reconstrução completa não têm número ([R09](../02_cenarios/a_rever/R09-resincronizacao-lenta-ou-bloqueada.md), [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md)).

## Onde está o detalhe

- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md) (6.5, 6.9)
- [ADR-063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md)
- [C29-disco-do-log-em-degradacao-longa.md](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)
- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

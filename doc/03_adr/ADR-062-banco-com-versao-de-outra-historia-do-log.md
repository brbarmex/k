# ADR-062 — Banco com versão de outra história do log: o apply não pode confirmar o que não gravou

**Status:** Pendente (aguarda o Bruno: decisões 1 a 4) · **Data:** 14/10/2026 · **Origem:** revisão geral do projeto de 14/10 · **Complementa:** [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) · **Precisa:** [ADR-015](ADR-015-wal-post-image-e-versao.md), [ADR-035](ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md), [ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md), [ADR-048](ADR-048-skip-por-item.md), [ADR-057](ADR-057-um-unico-modo-strong-consistency.md) · **Afeta o estado atual:** seção 2 (nova invariante) e 3.4, quando decidido · **Relacionado:** [ADR-063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md)

## Contexto

A versão de cada item é o índice do Raft (`_dr_v`, [ADR-015](ADR-015-wal-post-image-e-versao.md)). O applier grava com `PutItem` condicional `attribute_not_exists(_dr_v) OR _dr_v < :v` e trata `ConditionalCheckFailed` como "já aplicado", sem erro (`internal/adapter/out/dynamodb/store.go`, `ApplyBatch`). Isso deixa o replay idempotente, mas só enquanto **o banco e o log forem da mesma história**, ou seja, enquanto todo `_dr_v` gravado tiver vindo deste log.

Se o banco tiver um `_dr_v` de outra história maior que o índice atual, a condição falha, o item é tratado como aplicado, a marca d'água avança e **o cliente recebe ACK 200 de uma escrita que não está no banco**. Nada aparece em log, métrica ou `/status`.

Exemplo: o cluster antigo gravou `pk=cli-1` com `_dr_v = 900`. O cluster é recriado e o log recomeça. Uma escrita nova em `pk=cli-1` recebe o índice 5, a condição `900 < 5` falha, o applier conta como aplicado e o ACK sai. O banco continua com o valor antigo.

O [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) já reconhece que "a condição de versão não conhece a epoch" e resolve o retorno da AWS depois da promoção por **procedimento**: boot cercado, credenciais revogadas e banco recriado. Falta proteção no caminho dos dados e falta cobrir os outros cenários.

### Cenários

| Cenário | Efeito | Coberto hoje? |
|---|---|---|
| AWS volta depois da promoção da Azure | o banco tem órfãs com índices maiores que os da história da Azure | sim, pelo ADR-042, **se** o procedimento for seguido |
| Cluster recriado (WAL e log de replicação perdidos ou zerados) com as tabelas mantidas | o índice recomeça e toda escrita em chave existente é descartada em silêncio | não |
| Chave que muda de grupo Raft (resharding, hipótese do [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md)) ou dois shards configurados na mesma tabela | as sequências de índices colidem | não |
| DynamoDB restaurado por PITR ou backup para um ponto anterior, com o log seguindo | **o inverso**: o progresso do applier ([ADR-048](ADR-048-skip-por-item.md)) diz "aplicado até X", o banco não tem esses itens e o replay começa depois de X; os itens nunca voltam e a leitura forte devolve dado velho | não |

### A invariante que permite detectar

Num cluster saudável, **o `_dr_v` gravado nunca passa do gate**, porque nada é despachado ao banco antes do gate ([ADR-057](ADR-057-um-unico-modo-strong-consistency.md)). Então um `ConditionalCheckFailed` cuja versão gravada é maior que o gate atual é impossível nesta história: o banco veio de outra.

## Proposta

1. **Detector no apply (caminho dos dados).**
   - O `PutItem` condicional pede o item antigo no próprio erro (`ReturnValuesOnConditionCheckFailure: ALL_OLD`), sem leitura extra. Se o banco local não suportar, um `GetItem` só quando a condição falha, que é o caminho raro do replay.
   - Versão gravada **<= gate**: "já aplicado", como hoje.
   - Versão gravada **> gate**: o item é tratado como **venenoso** e o applier estaciona ([ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md)), com causa própria (`historia_divergente`) no `/status`, no `/readyz` e em métrica. A saída é humana.
   - Custo zero no caminho normal: o item antigo só volta quando a condição falha.
   - Cobre o cluster recriado, o resharding e o retorno da AWS sem o procedimento. Não cobre o PITR, nem a coincidência rara de duas histórias com a mesma versão na mesma chave.
2. **Marcador de história no banco, conferido no boot** (o boot cercado do ADR-042 virando mecanismo).
   - Uma **tabela de controle por célula**, fora das tabelas do cliente: não aparece no Scan e mantém a regra de não expor `_dr_v` ([ADR-051](ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)).
   - Guarda `cluster_id` (gerado na partida do zero e gravado também no WAL, junto do snapshot sintético do RT-03), `epoch` e a marca d'água aplicada, atualizada periodicamente como o progresso.
   - **No boot, antes de ligar o applier:**
     - `cluster_id` ou `epoch` diferentes: o applier não liga (fail-closed).
     - Marcador **atrás** do progresso local: o banco voltou no tempo. O replay recomeça do marcador; se o log já não tiver as entradas, a célula exige reconstrução em tabela sombra ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).
     - Sem marcador e com dados nas tabelas: banco de origem desconhecida, o applier não liga.
   - É a única das três defesas que cobre o PITR.
3. **Epoch dentro da versão** (`_dr_v = epoch × 10¹⁵ + índice`, ou dois atributos) **fica fora da v1**. Ela troca a perda silenciosa por **mistura silenciosa**: as chaves reescritas ficam novas e as outras continuam com dado velho ou órfão visível. Para o retorno da AWS, recriar o banco (ADR-042) é melhor. Só valeria se houvesse um caso legítimo de "história nova sobre banco antigo", e pelo desenho atual todos terminam em recriação.

## Decisões em aberto

1. **Detector no apply:** (a) estacionar o applier (ADR-038); (b) só registrar e alertar, sem parar. Recomendação: **a**. Alertar sem parar mantém o ACK de dado que não foi gravado.
2. **Marcador no boot:** (a) tabela de controle por célula; (b) item reservado dentro de cada tabela do cliente, filtrado no Scan e no GetItem; (c) não fazer na v1. Recomendação: **a**.
3. **Banco atrás do progresso (PITR):** (a) replay a partir do marcador quando o log ainda tem as entradas, senão reconstrução; (b) sempre reconstrução. Recomendação: **a**.
4. **Epoch na versão:** (a) fora da v1; (b) fazer na v1. Recomendação: **a**.

## Alternativas consideradas

- **Manter só o procedimento do ADR-042:** depende de uma pessoa executar todos os passos no pior momento possível, e não cobre recriação do cluster, resharding nem PITR.
- **Ler a versão antes de cada escrita:** detecta tudo, mas dobra o custo de cada apply (RCU e latência). O detector só paga quando a condição falha.
- **Conferir o `cluster_id` a cada escrita:** mesmo problema de custo; o ADR-042 já decidiu conferir a epoch no boot e periodicamente, não por escrita.

## Consequências (se aprovado como recomendado)

- A perda silenciosa vira **applier estacionado** com causa explícita: indisponibilidade de escrita naquele shard e célula, até ação humana. É a troca certa num sistema com RPO 0.
- O detector depende do suporte a `ReturnValuesOnConditionCheckFailure` no DynamoDB (existe na AWS) e, na POC, no DynamoDB Local do LocalStack (**a verificar**). Sem suporte, o `GetItem` no caminho raro resolve.
- O Cosmos, pelo Cloud-Adapter, precisa do mesmo detector e do mesmo marcador.
- O marcador entra no escopo da reconstrução em tabela sombra: a tabela nova nasce com o marcador da história atual.
- A partida do zero passa a gerar e gravar o `cluster_id`; o [ADR-063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md) carrega o `cluster_id` e a `epoch` no snapshot, para não depender do começo do log.

## Teste que prova (POC)

- Com a POC rodando e dados gravados, apagar só os volumes do Raft e do log de replicação, mantendo o LocalStack, e subir de novo. Uma escrita numa chave existente precisa estacionar o applier (decisão 1a), em vez de responder 200.
- Teste de unidade do detector: versão gravada <= gate segue como "já aplicado"; versão gravada > gate estaciona.

## Onde está o detalhe

- [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md), [C30](../02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md), [X03](../02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md), [R13](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md)
- [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md), seções 6.3.1 e 6.6

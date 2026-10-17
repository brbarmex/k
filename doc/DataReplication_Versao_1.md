# DataReplication v1: documento principal

[Início](00_COMECE_AQUI.md) · [Estado atual](ESTADO_ATUAL.md) · [Arquitetura](01_arquitetura/README.md) · [Cenários](02_cenarios/INDEX.md) · [ADRs](03_adr/INDEX.md) · [Diagramas](04_diagramas/INDEX.md) · [Implementação](05_implementacao/README.md) · [Pendências](06_pendencias_e_riscos/README.md) · [Glossário](01_arquitetura/00_glossario.md)

**Estado:** 14/10/2026, com as decisões da revisão crítica (ADR-044 a ADR-054), o group commit por shard (ADR-056), o apply paralelo por chave (ADR-058) e o laço do Raft que nunca espera o apply, depois do teste de carga da POC (ADR-060); ADR-061 descartado e ADR-062 e ADR-063 pendentes. As regras vigentes estão consolidadas no [Estado atual](ESTADO_ATUAL.md) · **Dono:** Bruno · **Papel deste documento:** reunir tudo o que importa sobre o `DataReplication` v1 e apontar, a cada assunto, o arquivo com o detalhe. Cada seção termina com os links para o aprofundamento.

## Sumário

1. [O problema](#1-o-problema)
2. [A solução em uma página](#2-a-solução-em-uma-página)
3. [Requisitos, restrições e premissas](#3-requisitos-restrições-e-premissas)
4. [Decisões (ADRs)](#4-decisões-adrs)
5. [Arquitetura em resumo](#5-arquitetura-em-resumo)
6. [Fluxos principais](#6-fluxos-principais)
7. [Falhas e recuperação](#7-falhas-e-recuperação)
8. [Cenários: situação de cada um](#8-cenários-situação-de-cada-um)
9. [Implementação](#9-implementação)
10. [Pendências, riscos e perguntas em aberto](#10-pendências-riscos-e-perguntas-em-aberto)
11. [Definição de pronto da v1](#11-definição-de-pronto-da-v1)
12. [Mapa dos arquivos](#12-mapa-dos-arquivos)

---

## 1. O problema

As aplicações do cliente persistem dados no **DynamoDB** (AWS) por meio do AWS SDK. Esses dados são valores de negócio, inclusive monetários, e precisam sobreviver à **perda de uma nuvem inteira**. Hoje não existe garantia de que um dado confirmado na AWS esteja em outra nuvem no instante da confirmação.

O que se quer:

- **RPO = 0 em operação normal:** nenhuma escrita confirmada ao cliente pode desaparecer num desastre coberto pelo modelo.
- **RTO = Delta-T**, ainda sem número: será uma **distribuição medida em exercícios de falha**, não uma soma de percentis ([R18](02_cenarios/a_rever/R18-rto-nao-quantificado.md)). Se a AWS morrer em modo degradado, o RTO é no mínimo D + M ([ADR-044](03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)).
- **Sem mudar a lógica das aplicações:** elas continuam falando "DynamoDB" com o AWS SDK. Mudam o endpoint e o certificado de cliente do mTLS por aplicação ([ADR-052](03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md)); "só trocar o endpoint" não é mais verdade.
- **Baixa latência de cauda:** meta de overhead P99 ≤ 50 ms a ~3.000 TPS.
- **Autocura** para falhas de uma célula, sem humano; falhas que exigem humano ficam explícitas.

A organização usa arquitetura **celular** (células isoladas, como *bulkheads*). Um **shard** reúne células; dentro dele, uma líder coordena as escritas. A AWS é a nuvem primária e a **Azure** é a de failover; ambas somente em **São Paulo**.

> **Não é objetivo da v1:** `UpdateItem` com expressão, escritas condicionais e transações do DynamoDB, multirregião, terceiro provedor de nuvem.

Contexto e requisitos originais: [01_arquitetura/01_forma_do_app_e_escopo.md](01_arquitetura/01_forma_do_app_e_escopo.md).

## 2. A solução em uma página

Uma **camada intermediária**, o `DataReplication`, fica entre a aplicação e o banco. Ela fala o protocolo do DynamoDB e **só confirma a escrita depois que o dado está durável na AWS e na Azure**.

```mermaid
flowchart LR
    APP["Aplicações do cliente<br/>AWS SDK v2 ou superior"] --> DR["DataReplication<br/>uma por célula"]
    DR --> AWSDB[("DynamoDB<br/>AWS primária")]
    DR ==>|"link dedicado"| AZ["DataReplication na Azure<br/>learners do consenso"]
    AZ --> AZDB[("Cosmos DB<br/>Azure failover")]
    ROUTER["Compass-Router e Agent<br/>externos ao escopo"] -.-> APP
```

**Como funciona, em cinco ideias:**

1. **Célula = um cluster Kubernetes (EKS ou AKS) numa AZ.** No namespace do cliente rodam as apps e **um único** `DataReplication` (sem sidecar). Há 3 células na AWS e 3 na Azure.
2. **Consenso Raft só na AWS.** As 3 células AWS são votantes (`etcd-io/raft`). As 3 células Azure são **learners**: recebem o log e informam o índice **durável**, mas não votam.
3. **O gate decide o ACK:** `gate = min(commit do Raft, 2º maior índice durável dos learners Azure)`. Ele **nunca recua**. O ACK sai depois do gate e do apply no banco da célula da líder.
4. **Um único log por nó:** o do Raft (`wal` do etcd, fsync antes do ACK, num volume dedicado), que é a única fonte das entradas commitadas e também retém o que a Azure atrasada ainda precisa. Um banco por célula; a escrita lógica é aplicada nos **seis** bancos.
5. **Se a Azure ou o link cair**, a política da instalação decide ([ADR-045](03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)). Com `DEGRADED_POLICY=accept`, após 2 s a líder propõe `DEGRADE` com **validade D**: a AWS segue sozinha (**RPO > 0**) sinalizando `x-dr-mode: degraded` e **para sozinha ao fim de D**. Com `reject`, as escritas falham e o RPO é 0 sempre. A Azure **nunca se promove sozinha**: a promoção é humana e só é aceita depois de D + M ([ADR-044](03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)).

Detalhe: [D01](04_diagramas/D01-visao-do-sistema.md), [C01](02_cenarios/atualizados/C01-visao-geral.md), [C02](02_cenarios/atualizados/C02-dentro-da-celula.md).

## 3. Requisitos, restrições e premissas

### 3.1 Restrições duras

| Restrição | Valor |
|---|---|
| Nuvens e regiões | **Somente AWS e Azure, ambas só em São Paulo** (`sa-east-1` e Brazil South) |
| Papéis | AWS primária (DynamoDB); Azure failover (Cosmos DB via Cloud-Adapter) |
| RPO / RTO | RPO = 0 em operação normal; RTO = Delta-T (a definir) |
| Carga | ~3.000 TPS, escritas majoritariamente `PutItem` e `BatchWriteItem` |
| Manutenção | Derruba **uma célula por vez** |
| Rede | 3 clusters AWS na **mesma VPC**; **link dedicado físico** AWS↔Azure, com tráfego transitivo entre contas e VPCs |
| Capacidade | DynamoDB sob demanda; Cosmos em RU; 3 AZs por nuvem |
| Política de degradação | `DEGRADED_POLICY` obrigatória por instalação: `accept` (com lease D) ou `reject` ([ADR-045](03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)) |
| Implantação | **Uma instalação por cliente**, com conta AWS própria; o cabo dedicado é compartilhado ([ADR-043](03_adr/ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md), [C31](02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md)) |
| Linguagem e execução | Go 1.27, cloud-native em AKS e EKS, 12 Factors |

### 3.2 Metas

| Meta | Valor |
|---|---|
| Latência | Overhead P99 ≤ 50 ms (alvo interno 30 a 35 ms); a escrita no banco fica fora da soma e é publicada como `overhead + banco` ([ADR-036](03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md), [C24](02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md)) |
| Memória e partida | `DataReplication` ≤ 256 MiB RSS e partida em segundos |
| Autocura | Reiniciar qualquer processo sem perder dado ACKado |

### 3.3 Clientes e API

- **SDKs aceitos:** AWS SDK **Java 2.x, Go v2 e .NET v4**, identificados pelo `User-Agent` (período de alerta e depois rejeição). Os clientes fixam *timeout* de 30 s por tentativa e no máximo 3 tentativas.
- **API da v1:** `PutItem`, `DeleteItem`, `BatchWriteItem`, `GetItem`, `BatchGetItem`, `Query` e `Scan`. **Fora:** `UpdateItem` com expressão, escrita condicional e transações; o uso real de condicionais por cliente deve ser levantado antes de ele entrar ([ADR-051](03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)).
- **Uso informado:** alguns clientes usam **TTL**, poucos usam **Scan**, algumas **Query** usam **GSI**. Valores monetários trafegam como **String**.
- Entrada máxima de **4 MiB**; item do DynamoDB de até 400 KB ([R17](02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md)).

### 3.4 Fora do escopo do time (dependências externas)

O **Compass-Router** (gateway) e o **Agent** do control plane decidem para onde vai o tráfego; o router sobrevive à perda total da AWS e o Agent roda nas duas nuvens.

Detalhe: [01_forma_do_app_e_escopo](01_arquitetura/01_forma_do_app_e_escopo.md) (seção 2), [09_12factors_cloud_native_e_sdks](01_arquitetura/09_12factors_cloud_native_e_sdks.md).

## 4. Decisões (ADRs)

As regras vigentes, consolidadas por tema e com o status de cada uma na POC, estão no **[Estado atual](ESTADO_ATUAL.md)**. É o documento para saber como o sistema funciona hoje sem ler os ADRs um a um.

- **[Estado atual](ESTADO_ATUAL.md):** invariantes de segurança, regras por tema, o que mudou e o que está em aberto.
- **[Inventário dos ADRs](03_adr/INVENTARIO.md):** status efetivo de cada ADR, quem altera quem, as cadeias de leitura por tema e as contradições a decidir.
- **[Índice dos ADRs](03_adr/INDEX.md):** a lista completa, um arquivo por decisão, com contexto, decisão e consequências.

## 5. Arquitetura em resumo

### 5.1 Forma do app e da célula

Um **monolito modular** por célula (módulos `api`, `core`, Raft com o seu log, transporte, applier), um pod, uma porta de API, `QoS Guaranteed` e `PodDisruptionBudget` de 1. Organização hexagonal (*screaming architecture*): `domain`, `port`, `usecase`, `adapter/in|out`, `platform`, `bootstrap`; só `bootstrap` e `cmd` importam o Fx, e `depguard` barra o Raft fora do adaptador. A decisão de pod único traz o risco de reinício derrubar o endpoint da célula, mitigado por partida rápida, PDB e transferência de liderança.

→ [01_forma_do_app_e_escopo](01_arquitetura/01_forma_do_app_e_escopo.md), [D02](04_diagramas/D02-implantacao-da-celula.md), [D03](04_diagramas/D03-camadas-hexagonais.md), [ADR-016](03_adr/ADR-016-app-unico-por-celula-monolito-modular.md), [ADR-031](03_adr/ADR-031-hexagonal-e-bibliotecas.md).

### 5.2 Consenso, quórum e gate

`etcd-io/raft` com camada multi-grupo própria (Dragonboat é o plano B). *Joint consensus* foi descartado porque a eleição passaria a exigir maioria Azure, contradizendo o modo degradado. O gate é uma função pequena, modelada em **TLA+ antes do código** (P1 a P8, o gate monotônico é a primeira; o modelo foi ampliado com lease, promoção entre nuvens, troca de membros, dedupe, DELETE e a barreira de leitura). O progresso da Azure **só chega pelas respostas do Raft e só conta no termo atual**; quando uma líder nova assume, ela **reconstrói** o índice da maioria Azure com progresso confirmado no termo novo antes de ACKar ([ADR-046](03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)).

→ [03_consenso_raft_quorum_e_gate](01_arquitetura/03_consenso_raft_quorum_e_gate.md), [D05](04_diagramas/D05-gate-e-quorum.md), [X01](02_cenarios/complexos/X01-gate-e-troca-de-lider.md), [X07](02_cenarios/complexos/X07-gate-explicado-com-exemplos.md) (exemplos com índices), [ADR-017](03_adr/ADR-017-azure-learners-e-gate.md), [ADR-029](03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md), [ADR-030](03_adr/ADR-030-verificacao-formal-tla.md), [ADR-046](03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md).

### 5.3 O log do Raft, o fsync e o disco

| | Log do Raft |
|---|---|
| Dono | A biblioteca (pelo adaptador) |
| Armazenamento | `wal` do etcd, um por shard, num volume de bloco dedicado por nó ([ADR-019](03_adr/ADR-019-wal-proprio-volume-dedicado.md)) |
| Gravação | Antes do ACK, com fsync, em group commit por shard |
| Compactação | Só até `RaftCompactLimit` = mínimo entre o aplicado local, o índice durável da maioria Azure enquanto ela estiver na janela e F − 1 de uma reconstrução em curso, com folga limitada por `RAFT_RETAIN_MAX` (desligada na POC; [ADR-063](03_adr/ADR-063-snapshot-e-compactacao-do-log-do-raft.md), pendente) |
| Retenção para a Azure atrasada | No próprio log do Raft; teto de 70% do volume do Raft, medido no espaço retido; acima dele a Azure sai da janela e volta por reconstrução em tabela sombra ([ADR-041](03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)) |
| Quem lê | O Raft (inclusive o `MsgApp` para learners e seguidores atrasados) e o `CommitPump`, que entrega as entradas commitadas ao Applier |

O **fsync é o gargalo**: só o log do Raft está no caminho do ACK, com o `wal` do etcd (segmentos pré-alocados, `fdatasync`, CRC) em **volume de bloco dedicado** (NVMe local descartado). Esse é o **único log do nó** e a única fonte das entradas commitadas: um único fsync no caminho do ACK, nenhuma escrita em dobro, um volume por nó. O progresso do applier (índice e ordem da marca d'água) é um registro pequeno com CRC, sem fsync, no próprio volume do Raft; perdê-lo só alonga o replay, que é idempotente pela versão. **Falha de fsync é fatal**; disco perdido volta com **identidade nova** como learner e a reconstrução é manual assistida (`drctl rebuild`). Não existe ACK com quórum só em memória. **Group commit por shard** ([ADR-056](03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)): o buffer em memória só monta o lote; nenhum ACK, voto ou resposta de `AppendEntries` sai antes do fsync; a líder envia `MsgApp` antes de gravar (F3) no modo síncrono, e o `AsyncStorageWrites` entra só se o spike S1b mostrar ganho; o buffer tem teto de 32 MiB por shard, com `ThrottlingException` acima disso. **O laço do Raft nunca espera o apply** ([ADR-060](03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)): o commit é publicado como sinal e o `CommitPump` lê por faixa da memória do Raft e entrega ao Applier; a retenção para a Azure atrasada fica no `MemoryStorage` do Raft, e num modo degradado longo a memória do nó cresce com o atraso até o teto (risco a medir no spike S1); no teste de carga de 14/10 o canal bloqueante deixava um banco lento parar o heartbeat e derrubar a liderança ([C35](02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md)).

→ [03_consenso_raft_quorum_e_gate](01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.5.1), [04_wal_e_fsync](01_arquitetura/04_wal_e_fsync.md), [D04](04_diagramas/D04-commitpump-e-log-do-raft.md), [X04](02_cenarios/complexos/X04-partida-e-recuperacao-do-no.md), [ADR-019](03_adr/ADR-019-wal-proprio-volume-dedicado.md), [ADR-041](03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), [ADR-056](03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md), [ADR-060](03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md), [ADR-034](03_adr/ADR-034-fsync-fatal-e-identidade-nova.md), [ADR-063](03_adr/ADR-063-snapshot-e-compactacao-do-log-do-raft.md).

### 5.4 Dados: versão, DELETE, TTL, GSI e banco por célula

- O log guarda a **post-image** e a **versão = índice do Raft**. O applier grava só se `versão guardada < versão da entrada` (atributo reservado `_dr_v`): o *replay* é idempotente.
- **DELETE real** com ordem por chave no applier (o tombstone conflitaria com o TTL do cliente), protegido por uma **invariante formal** (com leituras abertas, o estado é a dobra do log) e por um protocolo de reconstrução em **tabela sombra** ([ADR-047](03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)). **TTL expira localmente** em cada célula e não é replicado. **Query em GSI é recusada** (o índice global nunca é forte; [ADR-050](03_adr/ADR-050-query-em-gsi-recusada.md)). **Scan** sem snapshot. `_dr_v` nunca aparece nas respostas.
- **Um banco por célula, mantido** ([ADR-054](03_adr/ADR-054-banco-por-celula-mantido.md)). O DynamoDB é regional: o ganho das seis cópias é isolamento lógico, não domínio físico de falha. O nome lógico troca o `CELL_ID` pelo marcador `{cell}` e tem geração para o cutover da reconstrução; todas as células têm as mesmas tabelas lógicas. Um único escritor por tabela (o applier da célula).

→ [05_dados_versao_delete_ttl_gsi_e_banco_por_celula](01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md), [X06](02_cenarios/complexos/X06-delete-ttl-gsi.md), [D08](04_diagramas/D08-uma-escrita-seis-bancos.md), [ADR-015](03_adr/ADR-015-wal-post-image-e-versao.md), [ADR-022](03_adr/ADR-022-banco-por-celula-nome-logico.md), [ADR-024](03_adr/ADR-024-delete-real-ttl-local-gsi.md), [C33](02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md).

### 5.5 Modo único: strong consistency

O `DataReplication` tem **um único modo**, sem segundo fluxo na escrita, na replicação ou no Raft ([ADR-057](03_adr/ADR-057-um-unico-modo-strong-consistency.md)):

- **Escrita:** apply no banco **só depois do gate**, **em paralelo por chave**: `APPLY_WORKERS` workers por shard (padrão 16), a mesma chave sempre no mesmo worker e na ordem do log. O `applied` é a **marca d'água contígua**, e o ACK sai quando ela passa do índice da escrita ([ADR-058](03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)). Uma chave quente fica limitada a uma gravação por vez. A líder recusa escrita com `ThrottlingException` (causa `atraso_apply`) quando `gate − aplicado` passa de `APPLY_LAG_MAX`, antes de entrar no log ([ADR-060](03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)).
- **Leitura:** sempre forte na tabela base. As aplicações enviam `ConsistentRead=true`; a leitura espera `min(ReadIndex, gate)` e o apply local, o que desacopla a leitura da saúde da Azure ([ADR-049](03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)). Sem barreira no prazo, `503` recuperável, nunca dado velho.
- **Query e Scan em GSI são recusados** ([ADR-050](03_adr/ADR-050-query-em-gsi-recusada.md)).
- Não há variável de modo (`READ_MODE_DEFAULT` e `WRITE_MODE` não existem).

→ [06_leitura_forte_escrita_serial_e_latencia](01_arquitetura/06_leitura_forte_escrita_serial_e_latencia.md), [C03](02_cenarios/atualizados/C03-escrita-strong.md), [C05](02_cenarios/atualizados/C05-leitura-strong.md), [X08](02_cenarios/complexos/X08-leitura-forte-com-exemplos.md) (exemplos com dados mock), [ADR-057](03_adr/ADR-057-um-unico-modo-strong-consistency.md), [ADR-058](03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md), [ADR-049](03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md), [ADR-050](03_adr/ADR-050-query-em-gsi-recusada.md).

### 5.6 Prazos em cascata e idempotência

Tudo precisa responder dentro do *timeout* do SDK. O prazo nasce uma vez, na borda, e desce como `context`:

| Camada | Prazo |
|---|---|
| SDK do cliente | 30 s por tentativa, até 3 tentativas |
| `REQUEST_DEADLINE` (API) | **7,5 s** |
| Forward para a líder (gRPC) | restante do prazo, até 3 s |
| `DEGRADE_AFTER` | **2 s** |
| Validade do modo degradado (D) | a definir pelo negócio (PEND-02); a AWS para sozinha ao fim |
| Cliente do banco no applier | 1 s por tentativa, até 2 |
| Margem de resposta | ≥ 500 ms; `IdleTimeout` do servidor 120 s |

Prazo estourado **depois do commit** vira `503 PENDING`: a escrita não se perde e o retry do SDK é absorvido pelo **dedupe na líder** (id estável do SDK, janela de **5 min**) e pela versão condicional. Fora da janela vale a semântica do DynamoDB, e a escrita condicional está fora da v1 ([ADR-051](03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)). A partida **recusa subir** com prazos fora de ordem.

→ [08_resiliencia_autocura_e_timeouts](01_arquitetura/08_resiliencia_autocura_e_timeouts.md) (9.4), [X05](02_cenarios/complexos/X05-timeouts-e-idempotencia.md), [C07](02_cenarios/atualizados/C07-timeout-depois-do-commit.md), [D07](04_diagramas/D07-prazos-em-cascata.md), [ADR-026](03_adr/ADR-026-dedupe-de-retry-id-estavel.md), [ADR-028](03_adr/ADR-028-timeouts-em-cascata.md).

### 5.7 Resiliência, autocura e cloud-native

- **Probes:** `startupProbe` cobre a recuperação do log; `readinessProbe` não depende de quórum; `livenessProbe` nunca olha o estado do cluster. Desligamento limpo transfere a liderança.
- **Backpressure** com os erros do DynamoDB que o SDK já entende; entradas que já estouraram o prazo são descartadas sem trabalho.
- **Autorização:** mTLS por aplicação, com lista identidade → tabelas e operações permitidas ([ADR-052](03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md), [C34](02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md)).
- **Comunicação:** gRPC entre células (mTLS), `vtprotobuf`, sem compressão. **Config:** YAML com `viper` e variáveis de ambiente obrigatórias. **Logs:** `zerolog`, sem log no caminho quente. **Métricas** por etapa em OpenTelemetry/Prometheus, em porta de administração separada.
- **12 Factors** com desvios inevitáveis (estado no disco do log). Imagem *distroless*, multi-arquitetura, assinada.

→ [02_comunicacao_grpc](01_arquitetura/02_comunicacao_grpc.md), [07_go_memoria_cpu_e_disco](01_arquitetura/07_go_memoria_cpu_e_disco.md), [08_resiliencia_autocura_e_timeouts](01_arquitetura/08_resiliencia_autocura_e_timeouts.md), [09_12factors_cloud_native_e_sdks](01_arquitetura/09_12factors_cloud_native_e_sdks.md), [10_pontos_adicionais_e_metricas](01_arquitetura/10_pontos_adicionais_e_metricas.md), [ADR-021](03_adr/ADR-021-grpc-entre-celulas.md).

## 6. Fluxos principais

### 6.1 Escrita (modo Strong)

```mermaid
flowchart TD
    A["App chama PutItem no DataReplication"] --> B["Parse, validação, nome lógico da tabela, dedupe"]
    B --> C{"Esta célula é a líder?"}
    C -->|"não"| D["Encaminha à líder, um salto"]
    C -->|"sim"| AD{"Admissão: apply local e buffer do Raft em dia?"}
    D --> AD
    AD -->|"não"| T["400 ThrottlingException: nada entrou no log, o SDK repete"]
    AD -->|"sim"| E["Propose no Raft: 2 de 3 votantes AWS com fsync"]
    E --> F["Learners Azure: 2 de 3 com fsync"]
    F --> G["Gate alcança o índice"]
    G --> H["Applier grava no banco da célula da líder, em paralelo por chave"]
    H --> L{"Lease válido, se degradado?"}
    L -->|"sim"| I["ACK ao cliente, com header se estiver degradado"]
    L -->|"não"| M["503 LEASE_EXPIRED"]
    G -.->|"Azure inalcançável por 2 s e política accept"| J["DEGRADE com validade D: gate usa só o commit Raft"]
    G -.->|"Azure inalcançável e política reject"| K["503 PENDING: sem ACK, RPO 0"]
    J --> H
```

Detalhe: [C03](02_cenarios/atualizados/C03-escrita-strong.md) (escrita), [C06](02_cenarios/atualizados/C06-follower-encaminha.md) (follower), [C32](02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) (partição e lease), [X07](02_cenarios/complexos/X07-gate-explicado-com-exemplos.md) (gate com exemplos). A admissão é do [ADR-060](03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md) e do [ADR-056](03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md). Regras e invariantes: [Estado atual](ESTADO_ATUAL.md), seção 3.2.

### 6.2 Estados do shard

```mermaid
stateDiagram-v2
    [*] --> Normal
    Normal --> Degradado: Azure ou link inalcançável por 2 s, política accept
    Degradado --> Parado: lease D expirou
    Parado --> Degradado: renovação humana
    Degradado --> Ressincronizando: Azure volta
    Parado --> Ressincronizando: Azure volta
    Ressincronizando --> Normal: lag baixo e estável, RESTORE
    Normal --> PromovidoAzure: AWS perdida, promoção humana
    Degradado --> PromovidoAzure: AWS perdida, perda declarada, só depois de D + M
    Parado --> PromovidoAzure: AWS perdida, só depois de D + M
    PromovidoAzure --> Normal: AWS volta e failback planejado
```

Detalhe: [C17](02_cenarios/atualizados/C17-maquina-de-estados.md), [D06](04_diagramas/D06-estados-do-shard-e-do-no.md).

### 6.3 Leitura forte

Toda leitura é forte; não há leitura eventual nem consulta por GSI. A célula, líder ou não, pede à líder o `ReadIndex` e o gate na mesma resposta, espera o próprio banco ter aplicado até `min(ReadIndex, gate)` e só então lê. Assim a leitura nunca devolve estado anterior a uma escrita confirmada nem uma escrita ainda não confirmada, e uma Azure lenta não atrasa a leitura.

```mermaid
flowchart TD
    A["App chama GetItem ou Scan na tabela base"] --> GSI{"Usa IndexName de GSI?"}
    GSI -->|"sim"| V["400 ValidationException"]
    GSI -->|"não"| L{"Líder conhecida em até 1 s?"}
    L -->|"não"| NL["503 NO_LEADER"]
    L -->|"sim"| GR{"Gate da líder pronto no termo atual?"}
    GR -->|"não"| GNR["503 GATE_NOT_READY"]
    GR -->|"sim"| B["Barreira: ReadIndex R e gate G da líder, na mesma resposta"]
    B --> W{"Banco local aplicou até min de R e G?"}
    W -->|"ainda não"| E["Espera o applier, até o prazo de 7,5 s"]
    E --> W
    E -.->|"prazo vencido"| RT["503 READ_TIMEOUT"]
    E -.->|"applier parado num item venenoso"| AS["503 APPLIER_STOPPED, na hora"]
    W -->|"sim"| R["Lê o banco da própria célula e responde, sem _dr_v"]
```

Detalhe: [C05](02_cenarios/atualizados/C05-leitura-strong.md) (leitura), [X08](02_cenarios/complexos/X08-leitura-forte-com-exemplos.md) (leitura forte com exemplos), [R16](02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md) (custo na follower). Decisões: [ADR-049](03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md), [ADR-050](03_adr/ADR-050-query-em-gsi-recusada.md), [ADR-057](03_adr/ADR-057-um-unico-modo-strong-consistency.md). Regras e invariantes: [Estado atual](ESTADO_ATUAL.md), seção 3.3.

## 7. Falhas e recuperação

| O que falha | O que acontece | RPO | Cenário |
|---|---|---|---|
| Uma célula AWS (a líder ou uma AZ) | Eleição entre as outras duas; a líder nova reconstrói o gate antes de ACKar | 0 | [C08](02_cenarios/atualizados/C08-queda-de-celula-lider-ou-az-aws.md) |
| Uma AZ da Azure | O gate usa 2 de 3 learners; sem impacto | 0 | [C09](02_cenarios/atualizados/C09-queda-de-az-azure.md) |
| Apps das células AWS fora, replicação viva | O tráfego vai para a Azure, que encaminha as escritas à líder na AWS (o link vira caminho quente) | 0 | [C10](02_cenarios/atualizados/C10-celulas-de-app-aws-fora.md) |
| Manutenção planejada | Transferência de liderança com barreira e pausa curta | 0 | [C11](02_cenarios/atualizados/C11-transferencia-planejada.md) |
| Link ou Azure caem, política `accept` | Após 2 s, `DEGRADE` com validade D; a AWS segue sozinha e **para ao fim de D** (`503 LEASE_EXPIRED`) | **> 0** | [C12](02_cenarios/atualizados/C12-modo-degradado.md), [C32](02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md), [X02](02_cenarios/complexos/X02-modo-degradado.md) |
| Link ou Azure caem, política `reject` | Sem ACK sem a Azure: `503 PENDING`; sem degradar | 0 | [C32](02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) |
| Azure volta | Catch-up do log, depois `RESTORE` com barreira | 0 depois | [C13](02_cenarios/atualizados/C13-ressincronizacao-da-azure.md) |
| AWS inteira cai (modo normal) | Promoção **humana** da Azure (com `accept`, só depois de D + M); nada confirmado se perde | 0 | [C14](02_cenarios/atualizados/C14-aws-cai-sem-degradacao.md), [X03](02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md) |
| AWS cai em degradado | Promoção humana só depois de D + M (RTO ≥ D + M), com **perda declarada** (órfãs) | > 0 | [C15](02_cenarios/atualizados/C15-aws-cai-em-degradacao.md) |
| AWS volta depois da promoção | Boot cercado, credenciais revogadas, learner com identidade nova e célula recriada; failback planejado | — | [C16](02_cenarios/atualizados/C16-aws-volta-apos-promocao.md), [C30](02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md) |
| O processo `DataReplication` reinicia | Replay do log do Raft a partir do progresso do applier; leitura reabre no ponto anterior | 0 | [C18](02_cenarios/atualizados/C18-partida-e-recuperacao-do-no.md), [C19](02_cenarios/atualizados/C19-falha-do-datareplication.md) |
| Disco: fsync falha, corrupção, perda | Falha fatal, identidade nova, reconstrução | 0 | [C21](02_cenarios/atualizados/C21-disco-fsync-e-reconstrucao.md) |
| App escreve direto no banco | Bloqueado por IAM; detectado por auditoria | quebra o RPO | [C20](02_cenarios/atualizados/C20-bypass-de-escrita-direta.md) |
| Pod do namespace tenta escrever em tabela que não é dele | `403` pela lista de autorização do mTLS | — | [C34](02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md) |
| Célula com banco suspeito ou perdido | Reconstrução em tabela sombra (cópia difusa + replay + cutover + checksum) | 0 | [C33](02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md) |
| Banco de uma célula lento (throttling, partição quente) | O Raft segue sem eleição; na líder, `ThrottlingException` com causa `atraso_apply` até o apply alcançar; numa follower, só alerta | 0 | [C35](02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md) |
| Cliente com SDK não suportado | Alerta e depois rejeição por `User-Agent` | — | [C22](02_cenarios/atualizados/C22-cliente-com-sdk-nao-suportado.md) |
| Link lento, mas abaixo de 2 s | Só alerta; RPO continua 0 e a latência sobe | 0 | [C25](02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) |
| Entrada que o banco recusa para sempre | O shard para; `SKIP` por item, sempre decidido por uma pessoa | 0 | [C26](02_cenarios/atualizados/C26-entrada-venenosa.md) |
| Eleição em curso, sem líder conhecida | Espera até 1 s no servidor e então `503` | 0 | [C27](02_cenarios/atualizados/C27-encaminhamento-sem-lider-conhecida.md) |
| Disco do log enche em degradação longa | Teto de 70%; Azure fora da janela e reconstrução completa | > 0 | [C29](02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md) |
| Duas AZs da AWS caem | O shard **para**; promoção humana (regra a decidir com o TLA+) | 0 em normal | [R06](02_cenarios/a_rever/R06-duas-azs-aws-caem.md) |

As falhas que **ainda não têm decisão completa** (órfãs, backlog do Cosmos, ressincronização, RTO, entre outras) estão em [`a_rever/`](02_cenarios/INDEX.md). Link lento, entrada venenosa, disco do log, fencing do banco, lote e líder ausente foram decididos em 13/10 (C23 a C31); a partição com lease, a reconstrução em tabela sombra e a autorização por aplicação vêm da revisão crítica (C32 a C34); o banco lento que não derruba a liderança vem do teste de carga de 14/10 (C35).

**Quem age em cada falha** (automático, assistido ou manual): matriz em [08_resiliencia_autocura_e_timeouts](01_arquitetura/08_resiliencia_autocura_e_timeouts.md).

## 8. Cenários: situação de cada um

Todos os cenários têm diagrama Mermaid. Totais: **34 atualizados**, **6 complexos** e **14 a rever**. Tabela completa: [02_cenarios/INDEX.md](02_cenarios/INDEX.md).

| Grupo | Quais | Observação |
|---|---|---|
| ✅ Atualizados | [C01 a C35](02_cenarios/INDEX.md#atualizados-modelo-v1) | Redesenhados no modelo v1 (learners, gate, log do Raft, prazos). **C23 a C31** vêm das decisões de 13/10, **C32 a C34** da revisão crítica e **C35** do teste de carga de 14/10 |
| 🧩 Complexos | [X01 a X08](02_cenarios/INDEX.md#complexos-detalhamento) | Gate e troca de líder, modo degradado, desastre e promoção, partida e recuperação do nó, timeouts e idempotência, delete/TTL/GSI, gate e leitura forte explicados com exemplos |
| ⚠️ A rever, com proposta | [R07](02_cenarios/a_rever/R07-link-oscilando.md) | Falta decisão do Bruno |
| 🟡 A rever, parcialmente tratados | [R03](02_cenarios/a_rever/R03-backlog-de-apply-no-cosmos.md), [R06](02_cenarios/a_rever/R06-duas-azs-aws-caem.md), [R09](02_cenarios/a_rever/R09-resincronizacao-lenta-ou-bloqueada.md), [R11](02_cenarios/a_rever/R11-api-de-promocao-depende-da-aws.md), [R12](02_cenarios/a_rever/R12-promocao-com-aws-viva.md), [R13](02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md), [R16](02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md), [R17](02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md), [R20](02_cenarios/a_rever/R20-custo-6x.md), [R22](02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) | Parte já coberta pelo desenho. O R06 e o R12 têm o desenho decidido (lease, ADR-044) e faltam D e a prova no TLA+ |
| ❓ A rever, sem resolução | [R10](02_cenarios/a_rever/R10-operador-lento-no-desastre.md), [R18](02_cenarios/a_rever/R18-rto-nao-quantificado.md), [R23](02_cenarios/a_rever/R23-hipoteses-gerais.md) | Dependem do negócio ou de medição. O R18 tem o método decidido (distribuição medida em exercícios) |

## 9. Implementação

- **Código:** Go 1.27, hexagonal, injeção por Fx (só no `bootstrap`), `fasthttp`, `grpc-go` + `vtprotobuf`, `zerolog`, `viper`, `etcd-io/raft/v3` com o `wal` do etcd e group commit por shard (ADR-056). Guia: [05_implementacao/README.md](05_implementacao/README.md).
- **Estrutura de pastas e regras de lint:** [01_principios_bibliotecas_e_pastas](05_implementacao/01_principios_bibliotecas_e_pastas.md).
- **Desenho e diagramas de processo e de fluxo:** [02_desenho_e_diagramas](05_implementacao/02_desenho_e_diagramas.md).
- **Exemplos de código** (compilados) e **tabela de structs:** [03](05_implementacao/03_exemplos_de_codigo.md), [04](05_implementacao/04_tabela_de_structs.md), [codigo_exemplo/](05_implementacao/codigo_exemplo/README.md).
- **Testes, armadilhas e ordem de implementação:** [05](05_implementacao/05_testes_armadilhas_e_ordem.md). **Spikes S1, S1b e S2 a S7:** [06](05_implementacao/06_testes_e_spikes.md).

**Ordem de trabalho em cinco fases** (decidida na revisão crítica): (1) decisões e contratos de segurança; (2) modelo TLA+ ampliado, que nasce junto com a fase 1 porque o lease só vale se for modelado; (3) **núcleo mínimo verificável** (gate, lease, log do Raft, um caminho de banco) antes de qualquer outro; (4) os seis caminhos de banco, o rebuild em tabela sombra e a anti-entropia; (5) conformidade, game days, medição do RTO e prontidão. Detalhe: [05_implementacao](05_implementacao/README.md).

## 10. Pendências, riscos e perguntas em aberto

- **Perguntas para o Bruno** (formato `1a 2b`, com recomendação; a 16 foi confirmada na prática pela resposta 2a do group commit): [03_perguntas_para_o_bruno](06_pendencias_e_riscos/03_perguntas_para_o_bruno.md).
- **Bloqueante:** PEND-02, o aceite formal do RPO > 0 no modo degradado, agora **com o valor de D** (a validade do lease). **PEND-01** (promoção da Azure, split-brain) tem desenho decidido ([ADR-044](03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)) e fecha quando D e M forem aprovados e o modelo TLA+ provar a regra.
- **Pendências numeradas:** [01_pendencias](06_pendencias_e_riscos/01_pendencias.md). **Riscos priorizados:** [02_riscos](06_pendencias_e_riscos/02_riscos.md). **Revisões críticas:** [12/10](06_pendencias_e_riscos/revisao_critica_12_10.md) e [13/10](06_pendencias_e_riscos/revisao_critica_13_10.md).

Os riscos de maior atenção, incluindo os **bloqueantes de segurança** da revisão de 13/10: (1) duas pontas aceitando escrita na partição (mitigado pelo lease, a provar no TLA+); (2) prefixo do gate e reconstrução na troca de líder; (3) DELETE e replay sem prova; (4) RPO > 0 sem teto e sem aceite; (5) entrada venenosa trava o shard; (6) disco do log esgota em degradação longa; (7) Cosmos lento enchendo o disco do learner e degradando a AWS; (8) compatibilidade com o DynamoDB sem inventário nem suíte; (9) no modo síncrono com F3, uma resposta enviada antes do fsync faria o gate contar durabilidade inexistente (risco 34, [ADR-056](03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)).

## 11. Definição de pronto da v1

A definição de pronto vale **por instalação** (uma por cliente) e é uma lista de coisas a **provar**, não de coisas já feitas.

1. Um único app `DataReplication` por célula (módulos `api` e `core`, em portas e adaptadores), imagem *distroless*, rodando em AKS e EKS com os mesmos manifests.
2. `PutItem`, `DeleteItem`, `BatchWriteItem`, `GetItem`, `BatchGetItem` e `Query` funcionando contra os SDKs do contrato, com **inventário de chamadas do cliente** e **suíte de conformidade verde** (DynamoDB real, camada e Cosmos pelo Cloud-Adapter) para o uso real dele ([ADR-051](03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)).
3. Commit com gate (maioria AWS e maioria Azure), modo degradado **com lease** e retorno por entradas replicadas; `DEGRADED_POLICY` definida e testada nos dois valores.
4. Log com *group commit*, recuperação de queda e detecção de corrupção.
5. **Modelo TLA+ ampliado** verificado: gate, lease e promoção, troca de membros, dedupe, resultado desconhecido, DELETE e replay, barreira da leitura forte.
6. **SLO ponta a ponta** (latência, erro e recuperação), medido sobre a operação completa nos discos e no link reais: overhead P99 ≤ 50 ms a 3.000 TPS, publicado como `overhead + banco`. O **RTO é uma distribuição medida em exercícios de falha** (mediana, P95, pior caso), não uma soma de percentis.
7. Matar qualquer pod, particionar o link e encher o disco **não perdem dado ACKado**. A recuperação segue a **matriz automático, assistido e manual** ([08_resiliencia](01_arquitetura/08_resiliencia_autocura_e_timeouts.md)): promoção da Azure, `SKIP`, `drctl rebuild`, extração de órfãs e prorrogação de lease **são humanos** e entram na definição por exercício (game day), não por "sem humano".
8. Métricas por etapa e painel que provam as metas, incluindo as métricas de garantia por shard (`last_committed`, `last_azure_durable`, `last_acked`, `last_applied`, `last_reconciled`).
9. **Prazos:** em cada cenário de falha, nenhuma requisição ultrapassa `REQUEST_DEADLINE` + margem, e o resultado é sucesso ou erro definido; o processo recusa subir com prazos fora de ordem.
10. Autorização por aplicação (mTLS) e NetworkPolicy ativas; o IAM das aplicações sem escrita nas tabelas.
11. Código organizado como no guia de implementação (hexagonal, `depguard` no CI, Raft e Fx isolados), com testes de concorrência para o gate, o lease e o SKIP por item.

## 12. Mapa dos arquivos

| Pasta | Conteúdo |
|---|---|
| [`01_arquitetura/`](01_arquitetura/README.md) | Detalhe de arquitetura em 10 arquivos, mais o [glossário](01_arquitetura/00_glossario.md) |
| [`02_cenarios/`](02_cenarios/INDEX.md) | `atualizados/` (C01–C35), `complexos/` (X01–X08), `a_rever/` (14 cenários ainda sem decisão completa), cada um com diagrama |
| [`03_adr/`](03_adr/INDEX.md) | 58 decisões (ADR-001 a ADR-063), um arquivo cada, e o [inventário](03_adr/INVENTARIO.md) com o status efetivo de cada uma |
| [`04_diagramas/`](04_diagramas/INDEX.md) | Diagramas de arquitetura e o catálogo de todos os 86 diagramas do pacote |
| [`05_implementacao/`](05_implementacao/README.md) | Guia de código, exemplos compilados, structs, testes e spikes |
| [`06_pendencias_e_riscos/`](06_pendencias_e_riscos/README.md) | Perguntas, pendências, riscos e revisão crítica |

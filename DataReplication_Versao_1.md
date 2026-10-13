# DataReplication v1: documento principal

[Início](00_COMECE_AQUI.md) · [Arquitetura](01_arquitetura/README.md) · [Cenários](02_cenarios/INDEX.md) · [ADRs](03_adr/INDEX.md) · [Diagramas](04_diagramas/INDEX.md) · [Implementação](05_implementacao/README.md) · [Pendências](06_pendencias_e_riscos/README.md) · [Glossário](01_arquitetura/00_glossario.md)

**Estado:** 13/10/2026 · **Dono:** Bruno · **Papel deste documento:** reunir tudo o que importa sobre o `DataReplication` v1 e apontar, a cada assunto, o arquivo com o detalhe. Cada seção termina com os links para o aprofundamento.

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
- **RTO = Delta-T**, ainda sem número (a definir com testes, [R18](02_cenarios/a_rever/R18-rto-nao-quantificado.md)).
- **Sem mudar as aplicações:** elas continuam falando "DynamoDB" com o AWS SDK; só o endpoint muda.
- **Baixa latência de cauda:** meta de overhead P99 ≤ 50 ms a ~3.000 TPS.
- **Autocura** para falhas de uma célula, sem humano; falhas que exigem humano ficam explícitas.

A organização usa arquitetura **celular** (células isoladas, como *bulkheads*). Um **shard** reúne células; dentro dele, uma líder coordena as escritas. A AWS é a nuvem primária e a **Azure** é a de failover; ambas somente em **São Paulo**.

> **Não é objetivo da v1:** plataforma multi-cliente com muitos shards, `UpdateItem` com expressão, escritas condicionais e transações do DynamoDB, multirregião, terceiro provedor de nuvem.

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
4. **Dois logs:** o do Raft (curto, fsync antes do ACK) e o de replicação (nosso, longo, em lote depois do commit). Um banco por célula; a escrita lógica é aplicada nos **seis** bancos.
5. **Se a Azure ou o link cair**, após 2 s a líder propõe `DEGRADE`: a AWS segue sozinha (**RPO > 0**), sinalizando `x-dr-mode: degraded`. A Azure **nunca se promove sozinha**: a promoção é sempre humana.

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
| Implantação | **Uma instalação por cliente**, com conta AWS própria; o cabo dedicado é compartilhado ([ADR-043](03_adr/ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md), [C31](02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md)) |
| Linguagem e plataforma | Go 1.27, cloud-native em AKS e EKS, 12 Factors |

### 3.2 Metas

| Meta | Valor |
|---|---|
| Latência | Overhead P99 ≤ 50 ms (alvo interno 30 a 35 ms); a escrita no banco fica fora da soma e é publicada como `overhead + banco` ([ADR-036](03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md), [C24](02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md)) |
| Memória e partida | `DataReplication` ≤ 256 MiB RSS e partida em segundos |
| Autocura | Reiniciar qualquer processo sem perder dado ACKado |

### 3.3 Clientes e API

- **SDKs aceitos:** AWS SDK **Java 2.x, Go v2 e .NET v4**, identificados pelo `User-Agent` (período de alerta e depois rejeição). Os clientes fixam *timeout* de 30 s por tentativa e no máximo 3 tentativas.
- **API da v1:** `PutItem`, `DeleteItem`, `BatchWriteItem`, `GetItem`, `BatchGetItem`, `Query` e `Scan`. **Fora:** `UpdateItem` com expressão, escrita condicional e transações.
- **Uso informado:** alguns clientes usam **TTL**, poucos usam **Scan**, algumas **Query** usam **GSI**. Valores monetários trafegam como **String**.
- Entrada máxima de **4 MiB**; item do DynamoDB de até 400 KB ([R17](02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md)).

### 3.4 Fora do escopo do time (dependências externas)

O **Compass-Router** (gateway) e o **Agent** do control plane decidem para onde vai o tráfego; o router sobrevive à perda total da AWS e o Agent roda nas duas nuvens.

Detalhe: [01_forma_do_app_e_escopo](01_arquitetura/01_forma_do_app_e_escopo.md) (seção 2), [09_12factors_cloud_native_e_sdks](01_arquitetura/09_12factors_cloud_native_e_sdks.md).

## 4. Decisões (ADRs)

Um arquivo por decisão, com contexto, decisão e consequências. Índice completo: [03_adr/INDEX.md](03_adr/INDEX.md).

### Estrutura e escopo

| ADR | Decisão | Status |
|---|---|---|
| [ADR-001](03_adr/ADR-001-camada-intermediaria-agnostica.md) | Camada intermediária agnóstica a cloud e a banco | Confirmado (requisito) |
| [ADR-002](03_adr/ADR-002-shards-logicos-celulas-ativo-passivo.md) | Shards lógicos, células e ativo/passivo | Confirmado (requisito) |
| [ADR-003](03_adr/ADR-003-aws-primaria-azure-failover.md) | AWS primária e Azure como failover, ambas só em São Paulo | Confirmado (restrição dura) |
| [ADR-004](03_adr/ADR-004-dynamodb-primeiro-adaptador.md) | DynamoDB como primeiro adaptador; Cosmos pelo Cloud-Adapter | Confirmado (requisito) |
| [ADR-016](03_adr/ADR-016-app-unico-por-celula-monolito-modular.md) | Um único app DataReplication por célula, monolito modular | Decidido (Bruno) |
| [ADR-031](03_adr/ADR-031-hexagonal-e-bibliotecas.md) | Arquitetura hexagonal e bibliotecas | Decidido (Bruno) |
| [ADR-032](03_adr/ADR-032-parse-json-completo-e-rust.md) | Parse completo do JSON na v1; Rust como alternativa registrada | Decidido (Bruno); problema conhecido |
| [ADR-043](03_adr/ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md) | Uma instalação por cliente; o cliente é conhecido na configuração | Decidido (Bruno) |

### Consenso, durabilidade e gate

| ADR | Decisão | Status |
|---|---|---|
| [ADR-005](03_adr/ADR-005-ack-exige-gate.md) | ACK só depois do gate (Raft AWS e maioria durável Azure) | Aceito (reformulado na v1) |
| [ADR-006](03_adr/ADR-006-consenso-por-shard-eleicao-na-aws.md) | Consenso por shard; eleição só na AWS | Aceito |
| [ADR-017](03_adr/ADR-017-azure-learners-e-gate.md) | Azure como learners, com gate | Decidido (Bruno) |
| [ADR-018](03_adr/ADR-018-dois-logs-raft-e-replicacao.md) | Dois logs separados: o do Raft e o de replicação | Decidido (Bruno) |
| [ADR-019](03_adr/ADR-019-wal-proprio-volume-dedicado.md) | WAL próprio em volume de bloco dedicado; sem ACK em memória | Decidido (Bruno) |
| [ADR-020](03_adr/ADR-020-raft-etcd-io-camada-multigrupo.md) | Raft: etcd-io/raft com camada multi-grupo própria | Decidido (Bruno) |
| [ADR-021](03_adr/ADR-021-grpc-entre-celulas.md) | gRPC entre as células | Decidido (Bruno) |
| [ADR-029](03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md) | O gate nunca recua; líder nova reconstrói antes de ACKar | Decidido (Bruno) |
| [ADR-030](03_adr/ADR-030-verificacao-formal-tla.md) | Modelo TLA+ do gate antes do código do gate | Decidido (Bruno) |
| [ADR-034](03_adr/ADR-034-fsync-fatal-e-identidade-nova.md) | Falha de fsync é fatal; disco perdido volta com identidade nova | Decidido (Bruno) |
| [ADR-007](03_adr/ADR-007-sidecar-e-coordenacao-compartilhada.md) | Sidecar com coordenação compartilhada | Substituído pelo ADR-016 |
| [ADR-008](03_adr/ADR-008-terceiro-dominio.md) | Terceiro domínio com WAL integral | Descartado |
| [ADR-011](03_adr/ADR-011-witness-manifesto-snapshots.md) | Witness = manifesto assinado, snapshots e WAL | Mantido |

### Dados, modos e API

| ADR | Decisão | Status |
|---|---|---|
| [ADR-009](03_adr/ADR-009-eventual-e1.md) | Modo Eventual = E1 (mesma escrita, leitura local) | Decidido (Bruno) |
| [ADR-012](03_adr/ADR-012-sem-downgrade-strong-eventual.md) | Sem downgrade automático de Strong para Eventual | Confirmado (requisito) |
| [ADR-015](03_adr/ADR-015-wal-post-image-e-versao.md) | O log guarda a post-image e a versão do item | Aceito |
| [ADR-022](03_adr/ADR-022-banco-por-celula-nome-logico.md) | Um banco por célula; nome lógico com {cell} | Decidido (Bruno) |
| [ADR-023](03_adr/ADR-023-modos-de-escrita-e-leitura.md) | Modos de escrita (Strong serial, Eventual paralelo) e de leitura (padrão forte) | Decidido (Bruno) |
| [ADR-024](03_adr/ADR-024-delete-real-ttl-local-gsi.md) | DELETE real com ordem por chave; TTL local; GSI | Decidido (Bruno) |
| [ADR-025](03_adr/ADR-025-escopo-da-api-v1-e-sdks.md) | Escopo da API v1 e SDKs suportados | Decidido (Bruno) |
| [ADR-026](03_adr/ADR-026-dedupe-de-retry-id-estavel.md) | Dedupe de retry na líder por identificador estável | Decidido (Bruno) |
| [ADR-035](03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md) | BatchWriteItem vira uma entrada; o apply é por item e condicional | Decidido (Bruno) |
| [ADR-036](03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md) | O SLO de 50 ms exclui a escrita no banco; publicado como overhead + banco | Decidido (Bruno) |

### Operação e falhas

| ADR | Decisão | Status |
|---|---|---|
| [ADR-010](03_adr/ADR-010-follower-encaminha-para-lider.md) | Follower encaminha para a líder | Aceito (Bruno, 11/10/2026) |
| [ADR-013](03_adr/ADR-013-transferencia-planejada-de-lideranca.md) | Transferência planejada de liderança | Aceito (pausa como janela de exceção) |
| [ADR-014](03_adr/ADR-014-modo-degradado-opcao-b.md) | Modo degradado: a AWS segue quando a Azure fica inalcançável (opção B) | Decidido (Bruno, 11/10/2026) |
| [ADR-027](03_adr/ADR-027-sinalizacao-do-modo-degradado.md) | Modo degradado sinalizado por header e métrica | Decidido (Bruno) |
| [ADR-028](03_adr/ADR-028-timeouts-em-cascata.md) | Prazos em cascata dentro do timeout do SDK | Decidido (Bruno); valores a validar |
| [ADR-033](03_adr/ADR-033-promocao-da-azure-humana.md) | Promoção da Azure sempre humana (provisório) | Provisório (PEND-01 aberta) |
| [ADR-037](03_adr/ADR-037-faixa-cinzenta-do-link-so-alerta.md) | Link lento na faixa cinzenta: só alertar, RPO continua 0 | Decidido (Bruno) |
| [ADR-038](03_adr/ADR-038-skip-de-entrada-venenosa-sempre-humano.md) | O SKIP de uma entrada venenosa é sempre aprovado por uma pessoa | Decidido (Bruno) |
| [ADR-039](03_adr/ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md) | Sem líder conhecida: esperar até 1 s no servidor e então 503 | Decidido (Bruno) |
| [ADR-040](03_adr/ADR-040-leitura-eventual-na-azure-acima-do-limite.md) | Leitura eventual na Azure acima do limite: redirecionar à AWS ou recusar | Decidido (Bruno) |
| [ADR-041](03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) | Disco do log: teto de 70% e reconstrução completa; sem S3 na v1 | Decidido (Bruno) |
| [ADR-042](03_adr/ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) | AWS que volta depois da promoção: boot cercado, credenciais revogadas, célula recriada | Decidido (Bruno) |

## 5. Arquitetura em resumo

### 5.1 Forma do app e da célula

Um **monolito modular** por célula (módulos `api`, `core`, log, Raft, transporte, applier), um pod, uma porta de API, `QoS Guaranteed` e `PodDisruptionBudget` de 1. Organização hexagonal (*screaming architecture*): `domain`, `port`, `usecase`, `adapter/in|out`, `platform`, `bootstrap`; só `bootstrap` e `cmd` importam o Fx, e `depguard` barra o Raft fora do adaptador. A decisão de pod único traz o risco de reinício derrubar o endpoint da célula, mitigado por partida rápida, PDB e transferência de liderança.

→ [01_forma_do_app_e_escopo](01_arquitetura/01_forma_do_app_e_escopo.md), [D02](04_diagramas/D02-implantacao-da-celula.md), [D03](04_diagramas/D03-camadas-hexagonais.md), [ADR-016](03_adr/ADR-016-app-unico-por-celula-monolito-modular.md), [ADR-031](03_adr/ADR-031-hexagonal-e-bibliotecas.md).

### 5.2 Consenso, quórum e gate

`etcd-io/raft` com camada multi-grupo própria (Dragonboat é o plano B). *Joint consensus* foi descartado porque a eleição passaria a exigir maioria Azure, contradizendo o modo degradado. O gate é uma função pequena, modelada em **TLA+ antes do código** (P1 a P8, o gate monotônico é a primeira). Quando uma líder nova assume, ela **reconstrói** o índice da maioria Azure consultando os learners antes de ACKar.

→ [03_consenso_raft_quorum_e_gate](01_arquitetura/03_consenso_raft_quorum_e_gate.md), [D05](04_diagramas/D05-gate-e-quorum.md), [X01](02_cenarios/complexos/X01-gate-e-troca-de-lider.md), [ADR-017](03_adr/ADR-017-azure-learners-e-gate.md), [ADR-029](03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md), [ADR-030](03_adr/ADR-030-verificacao-formal-tla.md).

### 5.3 Os dois logs, o fsync e o disco

| | Log do Raft | Log de replicação |
|---|---|---|
| Dono | A biblioteca (pelo adaptador) | O `DataReplication` |
| Gravação | Antes do ACK, com fsync | Depois do commit, em lote |
| Retenção | Curta; truncado só até o `DurableIndex` do outro | Longa; teto de 70% do volume |
| Quem lê | Só o Raft | Applier, Azure atrasada, `drctl`, reconstrução |

O **fsync é o gargalo**: WAL próprio, segmentado, pré-alocado, `fdatasync`, CRC32C, *group commit* sem *linger*, em **volume de bloco dedicado** (NVMe local descartado). **Falha de fsync é fatal**; disco perdido volta com **identidade nova** como learner e a reconstrução é manual assistida (`drctl rebuild`). Não existe ACK com quórum só em memória.

→ [03_consenso_raft_quorum_e_gate](01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.5.1), [04_wal_e_fsync](01_arquitetura/04_wal_e_fsync.md), [D04](04_diagramas/D04-dois-logs-e-commitpump.md), [X04](02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md), [ADR-018](03_adr/ADR-018-dois-logs-raft-e-replicacao.md), [ADR-019](03_adr/ADR-019-wal-proprio-volume-dedicado.md), [ADR-034](03_adr/ADR-034-fsync-fatal-e-identidade-nova.md).

### 5.4 Dados: versão, DELETE, TTL, GSI e banco por célula

- O log guarda a **post-image** e a **versão = índice do Raft**. O applier grava só se `versão guardada < versão da entrada` (atributo reservado `_dr_v`): o *replay* é idempotente.
- **DELETE real** com ordem por chave no applier (o tombstone conflitaria com o TTL do cliente). **TTL expira localmente** em cada célula e não é replicado. **GSI** forte só até o índice propagar. **Scan** sem snapshot.
- **Um banco por célula.** O nome lógico troca o `CELL_ID` pelo marcador `{cell}`; todas as células têm as mesmas tabelas lógicas. Um único escritor por tabela (o applier da célula).

→ [05_dados_versao_delete_ttl_gsi_e_banco_por_celula](01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md), [X06](02_cenarios/complexos/X06-delete-ttl-gsi.md), [D08](04_diagramas/D08-uma-escrita-seis-bancos.md), [ADR-015](03_adr/ADR-015-wal-post-image-e-versao.md), [ADR-022](03_adr/ADR-022-banco-por-celula-nome-logico.md), [ADR-024](03_adr/ADR-024-delete-real-ttl-local-gsi.md).

### 5.5 Modos de escrita e de leitura

| Modo | Escrita | Leitura |
|---|---|---|
| **Strong** | Apply no banco **em série, depois do gate** | `ReadIndex` na líder e espera do apply local |
| **Eventual** (E1) | Apply **em paralelo**; o ACK sai só depois da Azure | Leitura local, informa a defasagem |

`WRITE_MODE` e `READ_MODE_DEFAULT` (padrão **forte**) são variáveis **obrigatórias**; `ConsistentRead=true` é sempre forte. Não há *downgrade* automático de Strong para Eventual.

→ [06_modos_de_escrita_e_leitura_e_latencia](01_arquitetura/06_modos_de_escrita_e_leitura_e_latencia.md), [C03](02_cenarios/atualizados/C03-escrita-strong.md), [C04](02_cenarios/atualizados/C04-escrita-eventual.md), [C05](02_cenarios/atualizados/C05-leitura-strong-e-eventual.md), [ADR-009](03_adr/ADR-009-eventual-e1.md), [ADR-012](03_adr/ADR-012-sem-downgrade-strong-eventual.md), [ADR-023](03_adr/ADR-023-modos-de-escrita-e-leitura.md).

### 5.6 Prazos em cascata e idempotência

Tudo precisa responder dentro do *timeout* do SDK. O prazo nasce uma vez, na borda, e desce como `context`:

| Camada | Prazo |
|---|---|
| SDK do cliente | 30 s por tentativa, até 3 tentativas |
| `REQUEST_DEADLINE` (API) | **7,5 s** |
| Forward para a líder (gRPC) | restante do prazo, até 3 s |
| `DEGRADE_AFTER` | **2 s** |
| Cliente do banco no applier | 1 s por tentativa, até 2 |
| Margem de resposta | ≥ 500 ms; `IdleTimeout` do servidor 120 s |

Prazo estourado **depois do commit** vira `503 PENDING`: a escrita não se perde e o retry do SDK é absorvido pelo **dedupe na líder** (id estável do SDK, janela de **5 min**) e pela versão condicional. A partida **recusa subir** com prazos fora de ordem.

→ [08_resiliencia_autocura_e_timeouts](01_arquitetura/08_resiliencia_autocura_e_timeouts.md) (9.4), [X05](02_cenarios/complexos/X05-timeouts-e-idempotencia.md), [C07](02_cenarios/atualizados/C07-timeout-depois-do-commit.md), [D07](04_diagramas/D07-prazos-em-cascata.md), [ADR-026](03_adr/ADR-026-dedupe-de-retry-id-estavel.md), [ADR-028](03_adr/ADR-028-timeouts-em-cascata.md).

### 5.7 Resiliência, autocura e plataforma

- **Probes:** `startupProbe` cobre a recuperação do log; `readinessProbe` não depende de quórum; `livenessProbe` nunca olha o estado do cluster. Desligamento limpo transfere a liderança.
- **Backpressure** com os erros do DynamoDB que o SDK já entende; entradas que já estouraram o prazo são descartadas sem trabalho.
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
    C -->|"sim"| E["Propose no Raft: 2 de 3 votantes AWS com fsync"]
    D --> E
    E --> F["Learners Azure: 2 de 3 com fsync"]
    F --> G["Gate alcança o índice"]
    G --> H["Applier grava no banco da célula da líder"]
    H --> I["ACK ao cliente, com header se estiver degradado"]
    G -.->|"Azure inalcançável por 2 s"| J["DEGRADE: gate usa só o commit Raft"]
    J --> H
```

Detalhe: [C03](02_cenarios/atualizados/C03-escrita-strong.md) (Strong), [C04](02_cenarios/atualizados/C04-escrita-eventual.md) (Eventual), [C06](02_cenarios/atualizados/C06-follower-encaminha.md) (follower).

### 6.2 Estados do shard

```mermaid
stateDiagram-v2
    [*] --> Normal
    Normal --> Degradado: Azure ou link inalcançável por 2 s
    Degradado --> Ressincronizando: Azure volta
    Ressincronizando --> Normal: lag baixo e estável, RESTORE
    Normal --> PromovidoAzure: AWS perdida, promoção humana
    Degradado --> PromovidoAzure: AWS perdida, perda declarada
    PromovidoAzure --> Normal: AWS volta e failback planejado
```

Detalhe: [C17](02_cenarios/atualizados/C17-maquina-de-estados.md), [D06](04_diagramas/D06-estados-do-shard-e-do-no.md).

## 7. Falhas e recuperação

| O que falha | O que acontece | RPO | Cenário |
|---|---|---|---|
| Uma célula AWS (a líder ou uma AZ) | Eleição entre as outras duas; a líder nova reconstrói o gate antes de ACKar | 0 | [C08](02_cenarios/atualizados/C08-queda-de-celula-lider-ou-az-aws.md) |
| Uma AZ da Azure | O gate usa 2 de 3 learners; sem impacto | 0 | [C09](02_cenarios/atualizados/C09-queda-de-az-azure.md) |
| Apps das células AWS fora, replicação viva | O tráfego vai para a Azure, que encaminha as escritas à líder na AWS (o link vira caminho quente) | 0 | [C10](02_cenarios/atualizados/C10-celulas-de-app-aws-fora.md) |
| Manutenção planejada | Transferência de liderança com barreira e pausa curta | 0 | [C11](02_cenarios/atualizados/C11-transferencia-planejada.md) |
| Link ou Azure caem | Após 2 s, `DEGRADE`; a AWS segue sozinha | **> 0** | [C12](02_cenarios/atualizados/C12-modo-degradado.md), [X02](02_cenarios/complexos/X02-modo-degradado.md) |
| Azure volta | Catch-up do log, depois `RESTORE` com barreira | 0 depois | [C13](02_cenarios/atualizados/C13-ressincronizacao-da-azure.md) |
| AWS inteira cai (modo normal) | Promoção **humana** da Azure; nada confirmado se perde | 0 | [C14](02_cenarios/atualizados/C14-aws-cai-sem-degradacao.md), [X03](02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md) |
| AWS cai em degradado | Promoção humana com **perda declarada** (órfãs) | > 0 | [C15](02_cenarios/atualizados/C15-aws-cai-em-degradacao.md) |
| AWS volta depois da promoção | Boot cercado, credenciais revogadas, learner com identidade nova e célula recriada; failback planejado | — | [C16](02_cenarios/atualizados/C16-aws-volta-apos-promocao.md), [C30](02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md) |
| O processo `DataReplication` reinicia | Recuperação dos dois logs; leitura reabre no ponto anterior | 0 | [C18](02_cenarios/atualizados/C18-partida-e-recuperacao-do-no.md), [C19](02_cenarios/atualizados/C19-falha-do-datareplication.md) |
| Disco: fsync falha, corrupção, perda | Falha fatal, identidade nova, reconstrução | 0 | [C21](02_cenarios/atualizados/C21-disco-fsync-e-reconstrucao.md) |
| App escreve direto no banco | Bloqueado por IAM; detectado por auditoria | quebra o RPO | [C20](02_cenarios/atualizados/C20-bypass-de-escrita-direta.md) |
| Cliente com SDK não suportado | Alerta e depois rejeição por `User-Agent` | — | [C22](02_cenarios/atualizados/C22-cliente-com-sdk-nao-suportado.md) |
| Link lento, mas abaixo de 2 s | Só alerta; RPO continua 0 e a latência sobe | 0 | [C25](02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) |
| Entrada que o banco recusa para sempre | O shard para; `SKIP` sempre decidido por uma pessoa | 0 | [C26](02_cenarios/atualizados/C26-entrada-venenosa.md) |
| Eleição em curso, sem líder conhecida | Espera até 1 s no servidor e então `503` | 0 | [C27](02_cenarios/atualizados/C27-encaminhamento-sem-lider-conhecida.md) |
| Leitura eventual na Azure com defasagem alta | Redireciona à AWS se possível, senão recusa | — | [C28](02_cenarios/atualizados/C28-leitura-eventual-na-azure-defasada.md) |
| Disco do log enche em degradação longa | Teto de 70%; Azure fora da janela e reconstrução completa | > 0 | [C29](02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md) |
| Duas AZs da AWS caem | O shard **para**; promoção humana | 0 em normal | [R06](02_cenarios/a_rever/R06-duas-azs-aws-caem.md) |

As falhas que **ainda não têm decisão** (split-brain, órfãs, backlog do Cosmos, ressincronização, RTO, entre outras) estão em [`a_rever/`](02_cenarios/INDEX.md). Link lento, entrada venenosa, disco do log, fencing do banco, lote, líder ausente e leitura eventual defasada foram decididos em 13/10 (C23 a C31).

## 8. Cenários: situação de cada um

Todos os cenários têm diagrama Mermaid. Totais: **31 atualizados**, **6 complexos** e **14 a rever**. Tabela completa: [02_cenarios/INDEX.md](02_cenarios/INDEX.md).

| Grupo | Quais | Observação |
|---|---|---|
| ✅ Atualizados | [C01 a C31](02_cenarios/INDEX.md#atualizados-modelo-v1) | Redesenhados no modelo v1 (learners, gate, dois logs, prazos). **C23 a C31** vêm das decisões de 13/10 |
| 🧩 Complexos | [X01 a X06](02_cenarios/INDEX.md#complexos-detalhamento) | Gate e troca de líder, modo degradado, desastre e promoção, partida e dois logs, timeouts e idempotência, delete/TTL/GSI |
| ⚠️ A rever, com proposta | [R07](02_cenarios/a_rever/R07-link-oscilando.md), [R16](02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md), [R17](02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md), [R20](02_cenarios/a_rever/R20-custo-6x.md) | Falta decisão do Bruno |
| 🟡 A rever, parcialmente tratados | [R03](02_cenarios/a_rever/R03-backlog-de-apply-no-cosmos.md), [R09](02_cenarios/a_rever/R09-resincronizacao-lenta-ou-bloqueada.md), [R11](02_cenarios/a_rever/R11-api-de-promocao-depende-da-aws.md) | Parte já coberta pelo desenho |
| ❓ A rever, sem resolução | [R06](02_cenarios/a_rever/R06-duas-azs-aws-caem.md), [R10](02_cenarios/a_rever/R10-operador-lento-no-desastre.md), [R12](02_cenarios/a_rever/R12-promocao-com-aws-viva.md), [R13](02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md), [R18](02_cenarios/a_rever/R18-rto-nao-quantificado.md), [R23](02_cenarios/a_rever/R23-hipoteses-gerais.md) | Dependem de outros arquitetos, do negócio ou de medição. O R06 já tem o método decidido (modelar no TLA+) |
| 🆕 Achados novos desta revisão | [R22](02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) | A janela de dedupe pesa ~58 MiB |

## 9. Implementação

- **Código:** Go 1.27, hexagonal, injeção por Fx (só no `bootstrap`), `fasthttp`, `grpc-go` + `vtprotobuf`, `zerolog`, `viper`, `etcd-io/raft/v3`. Guia: [05_implementacao/README.md](05_implementacao/README.md).
- **Estrutura de pastas e regras de lint:** [01_principios_bibliotecas_e_pastas](05_implementacao/01_principios_bibliotecas_e_pastas.md).
- **Desenho e diagramas de processo e de fluxo:** [02_desenho_e_diagramas](05_implementacao/02_desenho_e_diagramas.md).
- **Exemplos de código** (compilados) e **tabela de structs:** [03](05_implementacao/03_exemplos_de_codigo.md), [04](05_implementacao/04_tabela_de_structs.md), [codigo_exemplo/](05_implementacao/codigo_exemplo/README.md).
- **Testes, armadilhas e ordem de implementação:** [05](05_implementacao/05_testes_armadilhas_e_ordem.md). **Spikes S1 a S7:** [06](05_implementacao/06_testes_e_spikes.md).

Ordem sugerida: modelo TLA+ e `domain`/`Gate` → portas e `WriteService` → log de replicação e conformidade → Raft sobre o log → `dynamohttp` e applier → `peergrpc` e learners → degradado, recuperação, transferência → Cosmos, `drctl`, observabilidade e testes de falha.

## 10. Pendências, riscos e perguntas em aberto

- **Perguntas para o Bruno** (formato `1a 2b`, com recomendação): [03_perguntas_para_o_bruno](06_pendencias_e_riscos/03_perguntas_para_o_bruno.md).
- **Bloqueante:** PEND-02, o aceite formal do RPO > 0 no modo degradado. **Com os outros arquitetos:** PEND-01 (promoção da Azure, split-brain), não reaberta aqui.
- **Pendências numeradas:** [01_pendencias](06_pendencias_e_riscos/01_pendencias.md). **Riscos priorizados:** [02_riscos](06_pendencias_e_riscos/02_riscos.md). **Revisão crítica de 12/10:** [revisao_critica_12_10](06_pendencias_e_riscos/revisao_critica_12_10.md).

Os cinco riscos de maior atenção: (1) entrada venenosa trava o shard; (2) disco do log esgota em degradação longa; (3) RPO > 0 sem teto e sem aceite; (4) split-brain na promoção; (5) Cosmos lento enchendo o disco do learner e degradando a AWS.

## 11. Definição de pronto da v1

1. Um único app `DataReplication` por célula (módulos `api` e `core`, em portas e adaptadores), imagem *distroless*, rodando em AKS e EKS com os mesmos manifests.
2. `PutItem`, `DeleteItem`, `BatchWriteItem`, `GetItem`, `BatchGetItem` e `Query` funcionando contra os SDKs do contrato.
3. Commit com gate (maioria AWS e maioria Azure), modo degradado e retorno por entradas replicadas.
4. Log com *group commit*, recuperação de queda e detecção de corrupção.
5. Overhead P99 ≤ 50 ms a 3.000 TPS, **medido** nos discos e no link reais.
6. Matar qualquer pod, particionar o link e encher o disco **não perdem dado ACKado** e se recuperam sem humano (exceto PEND-01 e H-DR1).
7. Métricas por etapa e painel que provam as metas.
8. **Prazos:** em cada cenário de falha, nenhuma requisição ultrapassa `REQUEST_DEADLINE` + margem, e o resultado é sucesso ou erro definido; o processo recusa subir com prazos fora de ordem.
9. Código organizado como no guia de implementação (hexagonal, `depguard` no CI, Raft e Fx isolados).

## 12. Mapa dos arquivos

| Pasta | Conteúdo |
|---|---|
| [`01_arquitetura/`](01_arquitetura/README.md) | Detalhe de arquitetura em 10 arquivos, mais o [glossário](01_arquitetura/00_glossario.md) |
| [`02_cenarios/`](02_cenarios/INDEX.md) | `atualizados/` (C01–C31), `complexos/` (X01–X06), `a_rever/` (14 cenários ainda sem decisão), cada um com diagrama |
| [`03_adr/`](03_adr/INDEX.md) | 43 decisões, um arquivo cada |
| [`04_diagramas/`](04_diagramas/INDEX.md) | Diagramas de arquitetura e o catálogo de todos os 78 diagramas do pacote |
| [`05_implementacao/`](05_implementacao/README.md) | Guia de código, exemplos compilados, structs, testes e spikes |
| [`06_pendencias_e_riscos/`](06_pendencias_e_riscos/README.md) | Perguntas, pendências, riscos e revisão crítica |

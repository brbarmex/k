# Especificação da POC local do DataReplication (REASONS Canvas)

[Início](../doc/00_COMECE_AQUI.md) · [Documento principal](../doc/DataReplication_Versao_1.md) · [Implementação](../doc/05_implementacao/README.md)

**Para:** execução no SPDD pelo Bruno · **Data:** 13/10/2026 · **Status:** revisada com as decisões do Bruno (só AWS com 3 células, busca pelo DR, Delete e Batch na fase 2)
**Formato:** REASONS Canvas (Requirements, Entities, Approach, Structure, Operations, Norms, Safeguards), confirmado pelo Bruno em 13/10/2026.

Esta especificação **não traz código**. Ela diz o que a POC precisa fazer, como verificar e quais regras não podem ser quebradas. Cada requisito tem um identificador e cada critério de aceite pode ser verificado por comando.

Convenções: **DEVE** é obrigatório; **PODE** é opcional; "DR" é o DataReplication; "célula" é um serviço DR do compose (1, 2 e 3, todas AWS); índices e versões são o índice do log do Raft.

> **Escopo decidido pelo Bruno (13/10/2026): só AWS, 3 células.** Não há Azure na POC. O gate continua implementado com a regra completa (commit do Raft e maioria Azure), mas, com a lista de learners vazia, ele vira o commit do Raft (G-08). A POC prova Raft, apply, propagação do gate e leitura forte entre as 3 células; ela **não** prova o RPO 0 entre nuvens. As regras do gate que dependem da Azure (G-01 a G-05, G-07) são provadas por testes de unidade com learners simulados (O-02), não pelo compose.

---

## R. Requirements (o problema e quando está pronto)

### R.1 Objetivo

Provar, numa máquina local com Docker, o conceito central do DataReplication entre as três células AWS: **uma escrita feita em qualquer célula só é confirmada (ACK) depois de commitada pelo Raft (2 de 3 células com fsync), liberada pelo gate e aplicada no banco da célula líder; depois disso, as três células aplicam o mesmo item, com a mesma versão, cada uma na sua tabela**. A leitura forte, feita em qualquer célula, devolve o item.

### R.2 Requisitos funcionais

| ID | Requisito |
|---|---|
| RF-01 | O projeto DEVE gerar uma imagem Docker do DR a partir de um `Dockerfile` na raiz da POC. |
| RF-02 | Um `docker-compose.yml` DEVE subir o LocalStack (só DynamoDB) e **três serviços DR** (`cell-1`, `cell-2`, `cell-3`), todos com a mesma imagem e numa só rede, simulando três clusters. |
| RF-03 | Cada célula DEVE ter **dois volumes locais** do Docker (driver `local`): um para o log do Raft (o "WAL") e outro para o log de replicação, como no [ADR-055](../doc/03_adr/ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md). Os dados DEVEM sobreviver a `docker compose restart` e `down` (sem `-v`). |
| RF-04 | Um `taskfile.yaml` (Task v3) DEVE oferecer: subir o compose, criar as tabelas, escrever um cliente via `awscli` no endpoint do DR, buscar os registros nas três tabelas via DR, ler via DR, inspecionar as tabelas direto no LocalStack, ver o estado das células, ver os logs, parar e limpar (lista na seção S.5). |
| RF-05 | Existem três tabelas no DynamoDB do LocalStack: `cadastro_cliente_cell_1`, `cadastro_cliente_cell_2` e `cadastro_cliente_cell_3`, com chave de partição `pk` (String), sem chave de ordenação, sem índices, cobrança sob demanda. |
| RF-06 | O cadastro do cliente DEVE ter `pk` e os campos fixos `nome`, `sobrenome`, `email` e `contato`. Só a `pk` muda a cada escrita: uma string com prefixo e o timestamp em nanossegundos (ex.: `cli-1791934579927602214`). |
| RF-07 | A escrita DEVE usar `aws dynamodb put-item` apontando para o endpoint HTTP do DR da célula escolhida, com o nome físico da tabela **daquela** célula (`cadastro_cliente_cell_<id>`). |
| RF-08 | A busca DEVE passar pelo DR: para cada uma das três células, um `aws dynamodb scan` no endpoint do DR daquela célula, na tabela daquela célula, com leitura forte. O stdout mostra o nome da tabela e os itens contidos nela, sem `_dr_v`. |
| RF-09 | A leitura via DR (`GetItem` com a `pk`) DEVE funcionar em qualquer célula, líder ou follower, com leitura forte (seção A.6), e NÃO devolve `_dr_v`. |
| RF-10 | A escrita feita numa célula follower DEVE ser encaminhada à líder (um salto, [ADR-010](../doc/03_adr/ADR-010-follower-encaminha-para-lider.md)) e ter o mesmo resultado de uma escrita feita na líder. |
| RF-11 | Cada célula DEVE expor um endpoint de administração com o seu estado (papel, líder, termo, commit, gate, aplicado, log de replicação, Azure ligada ou não). |
| RF-12 | Uma task de inspeção DEVE ler as três tabelas **direto no LocalStack**, mostrando `_dr_v`. É a prova física de que o mesmo item tem a mesma versão nos três bancos; a busca via DR não mostra a versão ([ADR-051](../doc/03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)). |

### R.3 Operações da API do DynamoDB na POC

| Operação | Na POC | Observação |
|---|---|---|
| `PutItem` | DEVE | Caminho de escrita do happy path |
| `GetItem` | DEVE | Leitura de uma `pk` |
| `Scan` na tabela base | DEVE | Caminho da busca; com `IndexName` é recusado ([ADR-050](../doc/03_adr/ADR-050-query-em-gsi-recusada.md)); `FilterExpression`, `Segment` e `ProjectionExpression` ficam fora |
| `DeleteItem`, `BatchWriteItem` | Fase 2 | Na POC respondem 400 `ValidationException` "não suportado nesta fase" |
| `UpdateItem`, `Query`, `TransactWriteItems`, condicionais | Fora | 400 `ValidationException` ([ADR-025](../doc/03_adr/ADR-025-escopo-da-api-v1-e-sdks.md)) |

### R.4 Fora do escopo da POC

Azure inteira (learners, Cosmos, Cloud-Adapter, promoção, link AWS↔Azure), vários shards (um shard só), dedupe de retry ([ADR-026](../doc/03_adr/ADR-026-dedupe-de-retry-id-estavel.md)), mTLS ([ADR-052](../doc/03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md)), modo degradado (política fixa `reject`), compactação do Raft e snapshots, truncamento do log de replicação, reconstrução em tabela sombra, TTL, GSI, lista de SDKs por User-Agent, métricas Prometheus (PODE), transferência de liderança (PODE). Fase 2: `DeleteItem` e `BatchWriteItem`.

### R.5 Definição de pronto

A POC está pronta quando, numa máquina com Docker, Task v3 e AWS CLI v2, partindo de volumes vazios, **todos os critérios de aceite da seção O.3 passam**, e `go test -race ./...` passa dentro do container de build, inclusive os testes do gate com learners simulados.

---

## E. Entities (o domínio da POC)

| Entidade | O que é | Onde vive |
|---|---|---|
| **Célula** | Um serviço DR do compose, com `CELL_ID` (1, 2, 3) e a sua tabela | Configuração |
| **Nó** | O participante do Raft, com `NODE_ID` igual ao `CELL_ID`. As 3 células são votantes; não há learners na POC | Configuração |
| **Shard** | O grupo Raft. A POC tem um só | `etcdraft` |
| **Entry** | A unidade replicada: índice, termo, tipo (`Write`, `Noop`), operações e um identificador da proposta | `domain/entry.go` |
| **Op** | Uma mutação já normalizada: tabela lógica, tipo, chave e imagem final do item (post-image, [ADR-015](../doc/03_adr/ADR-015-wal-post-image-e-versao.md)) | `domain/entry.go` |
| **Tabela lógica** | O nome com `{cell}` no lugar do id: `cadastro_cliente_cell_{cell}` ([ADR-022](../doc/03_adr/ADR-022-banco-por-celula-nome-logico.md)) | `domain/tablemap.go` |
| **KeySchema** | A chave primária da tabela, lida com `DescribeTable` na partida | `domain` (tipo) e `adapter/out/dynamodb` |
| **Gate** | O índice até onde o ACK e o apply podem ir (seção A.3) | `domain/gate.go` |
| **Progresso do learner** | (termo, índice durável, identidade) de um learner. Na POC só existe nos testes de unidade | `port.LearnerProgress` |
| **Progresso do applier** | (índice, ordem) do último item aplicado, sem fsync | `adapter/out/replog` |
| **Versão** | O atributo reservado `_dr_v`, igual ao índice da entrada | Banco da célula |
| **Cadastro do cliente** | `pk` (S), `nome` (S), `sobrenome` (S), `email` (S), `contato` (S) | Payload do taskfile |

Valores fixos do cadastro: `nome` = `Maria`, `sobrenome` = `Silva`, `email` = `maria.silva@exemplo.com`, `contato` = `+55 11 91234-5678`.

---

## A. Approach (a estratégia)

### A.1 Base de código

A POC DEVE partir do esboço em [`05_implementacao/codigo_exemplo`](../doc/05_implementacao/codigo_exemplo/README.md), reaproveitando `domain`, `port`, `usecase` (`WriteService`, `CommitPump`, `Applier`, `Checkpointer`, `DegradeService`), `adapter/out/replog`, `adapter/in/dynamohttp`, `platform` e `bootstrap`, e completando o que o esboço declara como ausente: adaptador `etcdraft`, transporte gRPC, `StateStore` do DynamoDB, parser do JSON do DynamoDB, leitura forte, propagação do gate e ligação de tudo no `bootstrap`. Os testes existentes do esboço DEVEM continuar passando, ajustados só onde a interface mudar.

O código DEVE aceitar learners pela configuração (`LEARNERS`), mesmo que a POC rode com a lista vazia. Assim, ligar a Azure depois é configuração, não reescrita.

### A.2 Fluxo de escrita (happy path)

```mermaid
sequenceDiagram
    autonumber
    participant CLI as awscli
    participant F as DR célula follower
    participant L as DR célula líder
    participant O as Outra follower
    participant DB as Tabela da célula
    CLI->>F: PutItem cadastro_cliente_cell_N
    F->>F: valida na borda e converte para tabela lógica
    F->>L: Forward via gRPC (um salto)
    L->>L: Propose da Entry
    L->>F: MsgApp (antes do fsync da líder, F3)
    L->>O: MsgApp
    L->>L: wal.Save com fsync
    F-->>L: MsgAppResp depois do fsync
    O-->>L: MsgAppResp depois do fsync
    L->>L: commit = 2 de 3 células
    L->>L: gate = commit (POC sem Azure)
    L->>DB: apply condicional por _dr_v na tabela da líder
    L-->>F: índice da entrada
    F-->>CLI: 200 com x-dr-index
    L->>F: gate propagado
    L->>O: gate propagado
    F->>DB: apply até o gate
    O->>DB: apply até o gate
```

1. A borda (HTTP compatível com DynamoDB) valida a requisição **antes** de propor (SG-07) e troca o nome físico pelo lógico.
2. Se a célula não é líder, encaminha a escrita inteira à líder por gRPC. Se não há líder conhecida, espera até `WAIT_LEADER` e responde 503 `NO_LEADER`.
3. A líder propõe uma `Entry` ao Raft e recebe o índice quando a entrada é commitada (2 de 3 células com fsync).
4. A líder espera o gate alcançar o índice e espera o applier da própria célula aplicar o índice.
5. Só então responde 200. Prazo estourado em qualquer espera vira 503 `PENDING` (resultado desconhecido).
6. As duas follower aplicam a mesma entrada nas suas tabelas, sem bloquear o ACK, mas nunca além do gate que a líder propagou.

### A.3 Regras do gate

O gate é o índice máximo que pode ser confirmado ao cliente e aplicado em qualquer banco. As regras G-01 a G-07 são as do produto e DEVEM estar no código; na POC, sem learners, vale a G-08.

| ID | Regra |
|---|---|
| G-01 | Na líder, **gate = min(commit do Raft, maioria Azure)**. A maioria Azure é o 2º maior índice durável entre os 3 learners, contando só os que confirmaram no termo atual. Com menos de 2 confirmados, a maioria Azure é 0 e o gate não anda. |
| G-02 | O gate é **monotônico**: nunca recua, nem na troca de líder nem quando um learner sai ([ADR-029](../doc/03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md)). |
| G-03 | Só conta progresso de learner etiquetado com o **termo atual da líder**. Quando o termo avança, o progresso dos learners é descartado e só volta a contar com confirmações no termo novo ([ADR-046](../doc/03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)). |
| G-04 | O progresso de um learner vem **só** de uma resposta de AppendEntries (`MsgAppResp`) sem rejeição, recebida de um id que está na lista de learners. Resposta de heartbeat não conta. Nenhum evento de progresso pode ser descartado: o canal bloqueia em vez de perder. |
| G-05 | O learner só envia `MsgAppResp` **depois do fsync** das entradas no próprio disco (RT-04). É isso que torna "índice confirmado" igual a "índice durável na Azure". |
| G-06 | Em toda célula que não é a líder, **gate = min(commit local do Raft, maior gate recebido da líder)**. A líder publica o gate às outras células a cada avanço e, sem avanço, a cada `GATE_PUBLISH_INTERVAL` (100 ms). Um gate recebido é um valor já provado, então a célula adota o máximo e o commit local continua limitando. Vale também na POC. |
| G-07 | O gate fica "pronto" numa líder nova só depois que 2 learners confirmarem no termo dela. Antes disso a líder não atende leitura forte (503 `GATE_NOT_READY`) ([ADR-049](../doc/03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md), item 3). |
| G-08 | **POC sem Azure** (lista de learners vazia): **gate = commit do Raft** e o gate está sempre pronto. O DR DEVE registrar um aviso na partida e o `/status` DEVE mostrar `azure: "off"`. Isso é RPO 0 só entre as células AWS e existe só na POC (SG-12). |
| G-09 | Quem espera o gate (escrita, applier, leitura) é removido da lista de espera quando o prazo dele acaba: as requisições não deixam registros para trás. |
| G-10 | Modo degradado fica desligado na POC (`DEGRADED_POLICY=reject`). Sem Azure ele nunca seria acionado. |

### A.4 Comportamento do Raft

| ID | Regra |
|---|---|
| RT-01 | Um grupo Raft (um shard) com votantes = as 3 células (ids 1, 2, 3). Commit = 2 de 3 com fsync. O código aceita learners pela configuração, mas a POC roda sem nenhum. Learner nunca vota e nunca vira líder ([ADR-033](../doc/03_adr/ADR-033-promocao-da-azure-humana.md)). |
| RT-02 | Biblioteca `go.etcd.io/raft/v3` e o `wal` do etcd (`go.etcd.io/etcd/server/v3/storage/wal`), um WAL por shard. Só o pacote `adapter/out/etcdraft` importa essas bibliotecas. |
| RT-03 | **Partida do zero:** todas as células nascem do mesmo snapshot sintético (índice 1, termo 1) com a mesma `ConfState` (votantes e, se houver, learners), gravado no WAL. É assim que os learners seriam declarados sem `ConfChange`. A primeira entrada útil tem índice 2. |
| RT-04 | **Ordem do laço do Ready (modo síncrono com F3, [ADR-056](../doc/03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)):** (1) só o `MsgApp` da líder sai antes do fsync (`domain.SplitBeforeSync`); (2) `wal.Save` grava HardState e entradas com um fdatasync (é o group commit do shard); (3) as entradas vão ao `MemoryStorage`; (4) as demais mensagens saem, inclusive `MsgAppResp` e votos; (5) as entradas commitadas vão ao `CommitPump`; (6) `Advance`. |
| RT-05 | Erro de `wal.Save` (fsync) é **fatal**: o processo sai com código diferente de zero e o Docker reinicia ([ADR-034](../doc/03_adr/ADR-034-fsync-fatal-e-identidade-nova.md)). Nunca se repete o fsync. |
| RT-06 | Parâmetros: tick 100 ms, `ElectionTick` 10, `HeartbeatTick` 1, `CheckQuorum` e `PreVote` ligados, `MaxSizePerMsg` 1 MiB, `MaxInflightMsgs` 256, `MaxUncommittedEntriesSize` 32 MiB, `DisableProposalForwarding` ligado (o encaminhamento é do DR, não do Raft). |
| RT-07 | `Propose` devolve o índice **quando a entrada é commitada**, casando a entrada pelo identificador da proposta guardado no payload. `ErrProposalDropped` vira 400 `ThrottlingException` (nada entrou no log). Prazo estourado vira `PENDING`. |
| RT-08 | Entradas vazias da eleição e de `ConfChange` são entregues ao `CommitPump` como `Noop`, para que os índices cheguem **contíguos** ao log de replicação, que recusa buraco. |
| RT-09 | **Reinício:** a célula reabre o WAL, recarrega snapshot, HardState e entradas, e reinicia com `Applied` = índice do snapshot. O Raft reentrega as entradas commitadas e o applier as pula pelo progresso salvo (replay idempotente). |
| RT-10 | Se a lista de votantes ou de learners da configuração diferir da `ConfState` gravada no WAL, a célula NÃO sobe e diz para apagar os volumes (`task limpar`). A POC não troca membros. |
| RT-11 | A POC não gera snapshot nem compacta o log do Raft (a memória cresce com o uso). Receber um snapshot de um par é erro. |
| RT-12 | **Transporte:** gRPC entre células ([ADR-021](../doc/03_adr/ADR-021-grpc-entre-celulas.md)) com quatro métodos: `Step` (lote de mensagens do Raft já serializadas), `Forward` (escrita encaminhada), `ReadBarrier` (ReadIndex e gate na mesma resposta) e `GateUpdate` (gate da líder). Uma fila por par, sem bloquear o laço do Ready; fila cheia descarta a mensagem e avisa o Raft (`ReportUnreachable`). Sem TLS na POC. |

### A.5 Comportamento do apply (LogApply)

| ID | Regra |
|---|---|
| AP-01 | O `CommitPump` recebe cada lote commitado e, nesta ordem: informa o termo e o commit ao gate; entrega o lote ao applier **pela memória**; grava o lote no log de replicação **sem fsync**. Um erro de escrita no log de replicação é fatal. |
| AP-02 | O progresso dos learners corre numa goroutine própria do `CommitPump`, por evento, sem depender de haver commit novo ([`usecase/replicate.go`](../doc/05_implementacao/codigo_exemplo/internal/usecase/replicate.go)). Na POC, sem learners, ela fica parada. |
| AP-03 | O applier é **serial** e na ordem do índice. Antes de cada entrada, espera `gate >= índice`. **Invariante: aplicado <= gate**, sempre, nas três células. |
| AP-04 | Cada item vira um `PutItem` **condicional** na tabela da célula, com `_dr_v` = índice e a condição `attribute_not_exists(_dr_v) OR _dr_v < :v`. `ConditionalCheckFailedException` significa "já aplicado" e não é erro ([ADR-015](../doc/03_adr/ADR-015-wal-post-image-e-versao.md), [ADR-035](../doc/03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md)). |
| AP-05 | A tabela física é a lógica com `{cell}` trocado pelo `CELL_ID` **da célula que aplica**: a mesma entrada vai para `cadastro_cliente_cell_1`, `_2` e `_3`. |
| AP-06 | Depois de cada item, o applier grava o progresso (índice, ordem) no volume do log de replicação, **sem fsync**. Perder o progresso só alonga o replay. |
| AP-07 | Erro transitório do banco (throttling, 5xx, rede) é repetido com backoff exponencial de 100 ms a 5 s, sem limite, com log a cada tentativa. Erro permanente (4xx que não seja a condição de versão) **para o applier do shard naquela célula**: o processo continua de pé, o `/readyz` fica falso, o `/status` mostra o erro e a entrada, e a saída é humana ([ADR-038](../doc/03_adr/ADR-038-skip-de-entrada-venenosa-sempre-humano.md)). |
| AP-08 | Entradas `Noop` só avançam o índice aplicado. |
| AP-09 | Na líder, o ACK espera o apply **da própria célula**. Nas follower o apply é assíncrono ao ACK. Por isso, logo depois do ACK, uma leitura **direta** no LocalStack (task `inspecionar`) pode ainda não mostrar o item nas três tabelas; a busca e a leitura via DR (A.6) sempre mostram. |
| AP-10 | O `Checkpointer` faz o fsync do log de replicação a cada `REPLOG_SYNC_INTERVAL` (1 s) e publica o índice durável. A compactação do Raft fica desligada na POC (o `RaftCompactor` não faz nada e registra em log). |

### A.6 Leitura forte (GetItem e Scan)

1. Toda leitura é forte ([ADR-057](../doc/03_adr/ADR-057-um-unico-modo-strong-consistency.md)); `ConsistentRead` ausente é tratado como verdadeiro.
2. Na líder: espera o gate ficar pronto (G-07; na POC está sempre pronto), faz `ReadIndex` (confirma a liderança com o quórum) e lê o gate **depois** do `ReadIndex`.
3. Na follower: pergunta à líder pelo gRPC `ReadBarrier`, que devolve `ReadIndex` e gate **na mesma resposta** ([R16](../doc/02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md)). A follower não usa um gate guardado.
4. Espera o apply local alcançar **min(ReadIndex, gate)** ([ADR-049](../doc/03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)) e lê a tabela da própria célula com `ConsistentRead=true`.
5. A barreira vale uma vez por chamada: no `Scan` paginado, cada página é uma chamada com a sua barreira.
6. A resposta nunca contém `_dr_v` ([ADR-051](../doc/03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)).

---

## S. Structure (onde cada coisa fica)

### S.1 Pastas

A POC fica numa pasta própria, com a estrutura de [01_principios_bibliotecas_e_pastas.md](../doc/05_implementacao/01_principios_bibliotecas_e_pastas.md), seção 3:

```text
poc/
├── Dockerfile
├── docker-compose.yml
├── taskfile.yaml
├── README.md                      # como rodar e o que cada task faz
├── go.mod / go.sum                # módulo example.com/datareplication, go 1.27
├── cmd/datareplication/main.go    # só chama o bootstrap
├── api/peerv1/                    # contrato gRPC entre células
└── internal/
    ├── domain/                    # entry, gate, lease, tablemap, retention, sendrule
    ├── port/                      # portas de entrada e saída
    ├── usecase/                   # write, read, apply, replicate, checkpoint, degrade, gatesync
    ├── adapter/
    │   ├── in/dynamohttp/         # API compatível com DynamoDB (fasthttp) + parser
    │   ├── in/peergrpc/           # servidor gRPC entre células
    │   ├── in/adminhttp/          # /livez /readyz /status (net/http)
    │   └── out/
    │       ├── etcdraft/          # ÚNICO que importa o Raft e o wal do etcd
    │       ├── peerclient/        # cliente gRPC: transporte, forward, barreira, gate
    │       ├── replog/            # log de replicação segmentado (já existe no esboço)
    │       └── dynamodb/          # StateStore com aws-sdk-go-v2
    ├── platform/config, logx
    └── bootstrap/                 # ÚNICO que importa o fx
```

### S.2 Portas novas ou alteradas em relação ao esboço

| Porta | Mudança |
|---|---|
| `Reader` | `Get(ctx, tabela, chave)` sem o parâmetro `strong` (sempre forte) e `Scan(ctx, tabela, limite, chave inicial)` |
| `Scanner` | Nova: varredura da tabela base no banco da célula |
| `BarrierClient` | Nova: pergunta à líder `ReadIndex` e gate na mesma resposta |
| `GatePublisher` | Nova: leva o gate da líder às outras células |
| `Catalog` | Nova: `KeySchema` por tabela lógica, lido na partida |
| `domain.Gate` | Ganha o gate propagado (G-06) e o modo sem Azure (G-08) sem mudar o comportamento atual |
| `domain.EntryKind` | Ganha `KindNoop` (RT-08) |

### S.3 Imagem (Dockerfile)

| ID | Requisito |
|---|---|
| D-01 | Multi-estágio. Build com `golang:1.27-alpine` (existe a 1.27.2), `CGO_ENABLED=0`, `-trimpath`, cache de módulos e de build com `--mount=type=cache`. |
| D-02 | Execução com `alpine` (para permitir `ls` nos volumes durante a POC), usuário não root fixo (uid 10001), diretórios `/var/lib/dr/raft` e `/var/lib/dr/replog` criados no build e pertencentes a esse usuário (assim o volume novo herda o dono). |
| D-03 | Expõe 8000 (API), 7000 (gRPC entre células) e 9090 (administração). `HEALTHCHECK` no `/livez`. |
| D-04 | O build não depende de nada fora do contexto da pasta `poc/`. A imagem é construída uma vez e usada pelas três células. |

### S.4 Compose

| ID | Requisito |
|---|---|
| C-01 | Serviço `localstack` com a imagem **fixada** `localstack/localstack:4.14.0` (versões novas da imagem da comunidade podem exigir token, SG-14), `SERVICES=dynamodb`, porta 4566, healthcheck em `/_localstack/health`. |
| C-02 | Serviços `cell-1`, `cell-2`, `cell-3` com a mesma imagem (`build: .` num deles e `image:` com o mesmo nome nos outros, para não construir três vezes), `depends_on` do LocalStack saudável, `restart: on-failure` e as variáveis da tabela S.6. |
| C-03 | Portas no host: API da célula N em `800N` (8001, 8002, 8003) e administração em `909N` (9091, 9092, 9093). A porta gRPC não é publicada. |
| C-04 | Volumes nomeados por célula: `cell-N-raft` em `/var/lib/dr/raft` e `cell-N-replog` em `/var/lib/dr/replog`, seis no total. O WAL do etcd fica num **subdiretório** (`/var/lib/dr/raft/wal`), porque o `wal.Create` renomeia o diretório e não pode renomear o ponto de montagem. |
| C-05 | `PEERS` e `LEARNERS` são iguais nas três células (a `ConfState` de partida tem de ser a mesma em todas, RT-03). Ficam declarados uma vez no compose (bloco de extensão `x-`) e reaproveitados. `LEARNERS` fica vazia. |

### S.5 Taskfile

| Task | O que faz | Critério |
|---|---|---|
| `subir` | `docker compose up -d --build`, depois `criar-tabelas`, depois espera o `/readyz` das três células | CA-01 |
| `criar-tabelas` | Cria as três tabelas no LocalStack se não existirem e espera ficarem ativas; idempotente | CA-02 |
| `escrever` | `put-item` via `awscli` no endpoint do DR da célula `CELL` (padrão 1), tabela `cadastro_cliente_cell_<CELL>`, payload da seção E; imprime a `pk` e o índice | CA-03, CA-04 |
| `buscar` | Para cada célula: imprime o nome da tabela e faz `scan` **no endpoint do DR daquela célula**, mostrando os itens | CA-03 |
| `ler` | `get-item` da `PK` informada no endpoint do DR de cada célula, imprimindo célula e item | CA-05 |
| `inspecionar` | Para cada tabela: imprime o nome e faz `scan` **direto no LocalStack**, mostrando os itens com `_dr_v` | CA-03 |
| `status` | Imprime o `/status` das três células | CA-01 |
| `wal` | Lista os arquivos dos dois volumes de uma célula (`CELL`) | CA-11 |
| `logs` | `docker compose logs -f` | |
| `parar` | `docker compose down` (mantém os volumes) | |
| `limpar` | `docker compose down -v` (apaga os volumes) | |
| `e2e` | Roda `subir`, `escrever`, `buscar`, `ler` e `inspecionar`, e falha com mensagem clara se algo não bate | CA-03 a CA-05 |

Requisitos do taskfile:

| ID | Requisito |
|---|---|
| T-01 | A `pk` é gerada pelo **template do próprio Task** (`now` em nanossegundos, avaliado uma vez por execução da task), e não por `date +%s%N`, que não funciona no macOS. Duas execuções seguidas geram `pk` diferentes. |
| T-02 | O comando do AWS CLI é uma variável (`AWS`, padrão `aws`), para quem preferir rodar o CLI em container. |
| T-03 | As credenciais são fixas de teste (`test`/`test`), região `sa-east-1`, `AWS_PAGER` vazio. Nenhuma credencial real. |
| T-04 | `escrever`, `buscar` e `ler` usam o nome físico da tabela da célula de destino. Escrever em `cell-2` com o nome `cadastro_cliente_cell_1` é recusado (CA-10). |
| T-05 | As tasks que chamam o DR rodam o AWS CLI com `AWS_MAX_ATTEMPTS=1` (variável, padrão 1). Sem isso o CLI repete o 503 sozinho, a CA-07 demora três prazos e gera entradas repetidas (risco 4). |
| T-06 | A lista de células (1 a 3) e de portas é uma variável do taskfile, para não repetir o laço em cada task. |

### S.6 Configuração (variáveis de ambiente, lidas com viper)

| Variável | Exemplo na célula 1 | Regra |
|---|---|---|
| `CELL_ID` | `1` | Obrigatória |
| `NODE_ID` | `1` | Obrigatória, maior que zero; na POC igual ao `CELL_ID` |
| `PEERS` | `1=cell-1:7000,2=cell-2:7000,3=cell-3:7000` | Votantes, igual nas três células |
| `LEARNERS` | vazia | Vazia = POC sem Azure (G-08). Com valor, 3 ids de learners |
| `TABLES` | `cadastro_cliente_cell_{cell}` | Lista separada por vírgula; cada uma com `{cell}` uma vez |
| `DYNAMODB_ENDPOINT` | `http://localstack:4566` | Vazio = AWS real |
| `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | `sa-east-1`, `test`, `test` | Só teste |
| `API_ADDR`, `PEER_ADDR`, `ADMIN_ADDR` | `:8000`, `:7000`, `:9090` | |
| `RAFT_DIR`, `REPLOG_DIR` | `/var/lib/dr/raft/wal`, `/var/lib/dr/replog` | C-04 |
| `DEGRADED_POLICY` | `reject` | Obrigatória (G-10) |
| `TABLE_WAIT` | `120s` | Espera pelas tabelas na partida |
| `GATE_PUBLISH_INTERVAL`, `REPLOG_SYNC_INTERVAL` | `100ms`, `1s` | |
| `CLIENT_TIMEOUT`, `REQUEST_DEADLINE`, `WAIT_LEADER`, `FORWARD_TIMEOUT` | padrões do esboço (30 s, 7,5 s, 1 s, 3 s) | Regras de coerência do `config.go` |

As variáveis do antigo modo eventual (`READ_MODE_DEFAULT`, `WRITE_MODE`, `MAX_STALENESS`) continuam derrubando a partida ([ADR-057](../doc/03_adr/ADR-057-um-unico-modo-strong-consistency.md)).

### S.7 Endpoint de administração

| Caminho | Resposta |
|---|---|
| `GET /livez` | 200 se o processo está de pé |
| `GET /readyz` | 200 só com tabela conferida, Raft rodando, líder conhecida e applier sem erro |
| `GET /status` | JSON com `celula`, `no`, `papel` (líder ou follower), `lider`, `termo`, `commit`, `gate`, `gatePronto`, `aplicado`, `azure` (`on`/`off`), `replog` (`ultimo`, `duravel`), `erroApply`; na líder também o `match` de cada par |
| `POST /transferir?para=N` | PODE: transferência planejada de liderança ([ADR-013](../doc/03_adr/ADR-013-transferencia-planejada-de-lideranca.md)) |

---

## O. Operations (passos e critérios de aceite)

### O.1 Ordem de implementação

| Passo | Entrega | Pronto quando |
|---|---|---|
| O-01 | Copiar o esboço para `poc/`, subir o `go.mod` para Go 1.27 e as bibliotecas da seção N | `go build ./...` e os testes do esboço passam |
| O-02 | `domain`: gate propagado (G-06), modo sem Azure (G-08), `KindNoop`, `TableMap` e `KeySchema` | Testes de tabela com `-race` cobrindo **todas** as regras G-01 a G-09, com learners simulados: maioria de 2 de 3, termo antigo ignorado, troca de termo zera o progresso, monotonia, gate pronto, propagado, sem Azure e limpeza dos que esperam |
| O-03 | `adapter/out/etcdraft` (RT-01 a RT-11) | Teste com 3 nós em memória: elege líder, commita, reinicia e reentrega. PODE incluir um learner simulado para provar que o `MsgAppResp` dele vira evento de progresso (G-04) |
| O-04 | `api/peerv1`, `peergrpc`, `peerclient` (RT-12) | Teste de ida e volta de cada método |
| O-05 | `adapter/out/dynamodb`: conversão do JSON do DynamoDB, `DescribeTable` na partida, apply condicional, `GetItem` e `Scan` sem `_dr_v` | Teste contra o LocalStack |
| O-06 | Parser e handler do `dynamohttp` (R.3, SG-07, SG-08) | Testes de cada recusa da seção SG |
| O-07 | `usecase/read.go` (A.6) e `usecase/gatesync.go` (G-06) | Testes com motor de consenso falso |
| O-08 | `adminhttp` (S.7) | |
| O-09 | `config` com viper (S.6) e `bootstrap` com fx | Teste de grafo com `fx.ValidateApp` |
| O-10 | `Dockerfile` (S.3) | `docker build` limpo |
| O-11 | `docker-compose.yml` (S.4) | `docker compose config` sem erro |
| O-12 | `taskfile.yaml` (S.5) | Critérios da O.3 |
| O-13 | `README.md` da POC: pré-requisitos, comandos, o que observar | |

### O.2 Pré-requisitos na máquina

Docker com Compose v2, Task v3, AWS CLI v2. Nada de Go no host (o build é no container).

### O.3 Critérios de aceite

| ID | Dado / Quando / Então |
|---|---|
| **CA-01** | **Dado** volumes vazios, **quando** rodo `task subir`, **então** em até 60 s as três células respondem 200 no `/readyz`, e o `task status` mostra exatamente uma célula líder, as três com o mesmo `lider` e o mesmo `termo`, e `azure: "off"`. |
| **CA-02** | **Dado** as tabelas já criadas, **quando** rodo `task criar-tabelas` de novo, **então** termina com sucesso sem erro de tabela existente. |
| **CA-03** | **Dado** o ambiente de pé, **quando** rodo `task escrever` (célula 1), **então** o comando sai com 0 e imprime a `pk` e o índice (`x-dr-index`); **logo em seguida** o `task buscar` mostra a `pk` nas três tabelas, com os mesmos `nome`, `sobrenome`, `email` e `contato`; e o `task inspecionar` mostra, nas três tabelas, o **mesmo `_dr_v`**, igual ao índice impresso (pode levar até 2 s, AP-09). |
| **CA-04** | **Dado** que a líder é a célula X, **quando** rodo `task escrever CELL=Y` com Y diferente de X, **então** o resultado é o mesmo da CA-03 (a escrita foi encaminhada), e o log de Y registra o encaminhamento. |
| **CA-05** | **Dada** uma `pk` escrita, **quando** rodo `task ler PK=<pk>`, **então** as três células devolvem o mesmo item, **sem** `_dr_v`. |
| **CA-06** | **Quando** rodo `task escrever` duas vezes, **então** as `pk` são diferentes e o `_dr_v` da segunda é maior que o da primeira. |
| **CA-07** | **Sem quórum não há ACK nem apply.** **Quando** paro duas células (`docker compose stop cell-2 cell-3`, se a líder for a 1) e escrevo na célula que sobrou, **então** a escrita responde 503 (`NO_LEADER` ou `PENDING`) depois do prazo, o `commit` e o `gate` não avançam e **nenhuma das três tabelas** recebe o item. **Quando** religo as duas, **então** o cluster volta a ter líder e uma nova escrita funciona normalmente. |
| **CA-08** | **Quando** reinicio uma follower (`docker compose restart cell-N`) depois de algumas escritas, **então** ela volta ao `/readyz`, alcança o mesmo `aplicado` da líder e os itens dela não mudam de `_dr_v` (replay idempotente). |
| **CA-09** | **Quando** paro a líder, **então** em até 5 s outra célula vira líder e `task escrever` numa das células restantes funciona. **Quando** a antiga líder volta, **então** ela alcança as escritas que perdeu. |
| **CA-10** | **Quando** envio `put-item` com um atributo `_dr_v`, **ou** sem o atributo `pk`, **ou** com `pk` numérica, **ou** para `cell-2` com a tabela `cadastro_cliente_cell_1`, **então** recebo 400 (`ValidationException` ou `ResourceNotFoundException`) e nada entra no log (o `commit` não muda). |
| **CA-11** | **Quando** rodo `task wal CELL=1`, **então** vejo arquivos `.wal` em `/var/lib/dr/raft/wal` e segmentos `.seg` e o arquivo `progress` em `/var/lib/dr/replog`. |
| **CA-12** | **Dados** volumes já criados, **quando** mudo `PEERS` ou `LEARNERS` e subo sem limpar, **então** a célula não sobe e o log pede `task limpar` (RT-10). |
| **CA-13** | **Quando** envio `delete-item` ou `batch-write-item`, **então** recebo 400 `ValidationException` "não suportado nesta fase". |
| **CA-14** | **Quando** rodo `go test -race ./...` no container de build, **então** todos os testes passam, inclusive os do gate com learners simulados (O-02). |

---

## N. Norms (padrões de engenharia)

| ID | Norma |
|---|---|
| N-01 | Go 1.27, módulo `example.com/datareplication`. Bibliotecas: `fasthttp` (API), `net/http` (administração), `zerolog`, `viper`, `fx` (só no `bootstrap` e no `cmd`), `google.golang.org/grpc`, `go.etcd.io/raft/v3`, `go.etcd.io/etcd/server/v3` (só o `wal`), `aws-sdk-go-v2` (`dynamodb` e `config`). Nenhuma outra sem justificativa no README. |
| N-02 | Regra de dependência para dentro: `adapter → usecase → domain` e `adapter → port`. `domain` só usa a biblioteca padrão. Só `etcdraft` importa o Raft e o wal; os outros adaptadores trocam mensagens do Raft como bytes. |
| N-03 | gRPC sem `.proto` gerado é aceitável na POC (codec de bytes crus e serviço declarado à mão em `api/peerv1`), desde que os quatro métodos de RT-12 estejam isolados nesse pacote. |
| N-04 | Logs em JSON no stdout, com `celula`, `no` e `termo`. Nada de log por requisição no caminho quente; logs de mudança de líder, de gate parado, de encaminhamento e de erro do applier. |
| N-05 | Erros da API no formato do DynamoDB (`__type` e `message`), serializados com `encoding/json`. 503 traz o motivo no header `x-dr-code`. O 200 do `PutItem` traz o índice no header `x-dr-index` (extensão da POC, ignorada pelos SDKs). |
| N-06 | O corpo da requisição do `fasthttp` é reutilizado depois do handler: tudo o que vai ao log é copiado antes. |
| N-07 | Comentários e mensagens de log em português, como no esboço. |
| N-08 | Toda estrutura compartilhada tem teste com `-race`. |

---

## S. Safeguards (o que não pode ser quebrado)

| ID | Salvaguarda |
|---|---|
| SG-01 | **Nenhum ACK antes de: commit do Raft (2 de 3), gate >= índice e apply na célula da líder.** |
| SG-02 | **Aplicado <= gate**, nas três células, sempre. Uma follower nunca aplica além do gate propagado (G-06). |
| SG-03 | **Nada sai antes do fsync**, exceto o `MsgApp` da líder (RT-04). Em especial, `MsgAppResp` e votos só depois do `wal.Save`. |
| SG-04 | O gate nunca recua e só conta learner no termo atual (G-02, G-03). |
| SG-05 | Erro de fsync no WAL do Raft derruba o processo (RT-05). |
| SG-06 | O apply é sempre condicional pela versão: reaplicar nunca regride um item. |
| SG-07 | **Validação antes do commit:** tudo o que o DynamoDB recusaria (chave ausente ou de tipo errado, item acima de 400 KB, atributo `_dr_v`, operação ou parâmetro fora do escopo) é recusado na borda com 400. Um item recusado pelo banco **depois** do commit vira entrada venenosa e para o shard. |
| SG-08 | O nome lógico é derivado pela **lista de tabelas permitidas** (físico de cada lógica na célula local, comparado ao pedido), nunca trocando o texto do `CELL_ID` no nome: com `CELL_ID=1`, uma troca de texto erraria em nomes como `t1_cell_1`. |
| SG-09 | Learner nunca vira líder pelo Raft; promoção da Azure é humana e fora da POC. |
| SG-10 | O laço do Ready nunca bloqueia no envio de rede (filas por par que descartam). |
| SG-11 | Prazos em cascata coerentes: `REQUEST_DEADLINE` < `CLIENT_TIMEOUT`; `FORWARD_TIMEOUT` < `REQUEST_DEADLINE`; `WAIT_LEADER` <= `FORWARD_TIMEOUT` ([ADR-028](../doc/03_adr/ADR-028-timeouts-em-cascata.md)). |
| SG-12 | O modo sem Azure (G-08) é **só da POC**: aviso na partida, `azure: "off"` no `/status` e um comentário no código dizendo que nunca vai para produção. Em produção, `LEARNERS` vazia derruba a partida. |
| SG-13 | Nenhuma credencial real em arquivo; nada de push para o repositório sem pedido do Bruno. |
| SG-14 | Imagem do LocalStack com versão fixa (`4.14.0`): a imagem da comunidade avisa que muda de modelo em 2026 e as versões novas podem exigir token. |

---

## Riscos e cenários ocultos da POC

1. **Sem Azure, o gate de ponta a ponta não é testado.** Com a lista de learners vazia, o gate é o commit do Raft: a POC prova RPO 0 entre as 3 células AWS, não entre nuvens. A parte da Azure (maioria 2 de 3, termo, monotonia, gate pronto) fica provada só nos testes de unidade (O-02, CA-14). Ligar a Azure depois é configuração (`LEARNERS`), mas o caminho do learner (fsync antes de responder, evento de progresso, gate esperando a Azure) só será exercitado de verdade nessa hora.
2. **O risco de "gate = commit" escapar para produção.** O modo sem Azure torna o ACK igual ao commit AWS. SG-12 exige que, fora da POC, a lista vazia derrube a partida.
3. **Applier lento trava o Raft.** A fila entre o laço do Ready, o `CommitPump` e o applier é limitada. Se o banco de uma célula fica lento ou o applier para (entrada venenosa), a fila enche, o laço do Ready espera, os heartbeats atrasam e a célula pode provocar eleição. Na POC, filas grandes resolvem. Em produção o applier precisa ler do log de replicação quando fica para trás, sem segurar o laço do Ready.
4. **Retry do AWS CLI sem dedupe.** O AWS CLI v2 repete 503 automaticamente. Uma escrita `PENDING` repetida vira outra entrada no log, com outro índice. O item final é o mesmo, mas o `_dr_v` fica com o índice da repetição. É o comportamento esperado sem o [ADR-026](../doc/03_adr/ADR-026-dedupe-de-retry-id-estavel.md). T-05 desliga o retry nas tasks.
5. **Um LocalStack para as três células.** As três tabelas estão no mesmo processo: não há isolamento de falha entre bancos. Serve para o conceito, não para medir.
6. **Leitura direta no LocalStack logo depois do ACK.** O ACK só espera o apply da líder (AP-09). Um `task inspecionar` imediato pode mostrar o item em 1 ou 2 tabelas. A busca via DR não tem esse problema, porque espera a barreira.
7. **Memória do Raft sem compactação** (RT-11): rodar a POC por horas sob carga faz o processo crescer. Fora do escopo do teste de conceito.
8. **Gate atrasado nas follower.** Cada follower só aplica até o último gate recebido. Se uma mensagem `GateUpdate` se perde, a reenvio periódica (G-06) cobre; o atraso máximo é `GATE_PUBLISH_INTERVAL`.

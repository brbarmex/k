# Princípios, bibliotecas e estrutura de pastas

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice da implementação](README.md)

Este guia diz **como o código é organizado**. As decisões de arquitetura (Raft, log, gate, modos) estão em [`01_arquitetura/`](../01_arquitetura/) e nos [ADRs](../03_adr/INDEX.md), e não são repetidas aqui. Decisões do Bruno: Go 1.27, Fx, `fasthttp`, `zerolog`, `viper` ([ADR-031](../03_adr/ADR-031-hexagonal-e-bibliotecas.md)).

## 1. Princípios

1. **Screaming architecture:** a árvore de pastas grita o que o sistema faz (`write`, `read`, `gate`, `degraded`, `apply`), não qual framework usa. Quem abre `internal/` entende o negócio antes de ver HTTP ou Raft.
2. **Ports and adapters (hexagonal):** o núcleo (`domain` + `usecase`) só conhece **interfaces** (`port`). HTTP, gRPC, etcd-raft, WAL, DynamoDB e Cosmos são adaptadores intercambiáveis. Isso materializa a decisão de poder trocar o Raft (5.5) sem tocar no núcleo.
3. **Lean:** poucas pastas, poucas camadas, nada de abstração sem uso. Uma interface só existe se há pelo menos dois motivos para trocar a implementação (produção e teste contam).
4. **Regra de dependência, sempre para dentro:** `adapter → usecase → domain`, `adapter → port`. O núcleo nunca importa um adaptador, nem `fx`, nem a lib de Raft.
5. **Caminho quente sem surpresa:** sem reflexão, sem log e sem alocação evitável entre a chegada da requisição e o ACK. O Fx e a reflexão ficam só na partida.
6. **Falha fechada, estado real:** configuração sem `DEGRADED_POLICY` (ou sem `DEGRADED_LEASE` com `accept`) não sobe ([ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)); política desconhecida recusa o `DEGRADE`; o header `x-dr-mode` vem do estado real do shard (lease ativo), nunca de um valor fixo; relógio, rede e disco entram por porta ou por função injetável, para que o lease e os prazos sejam testáveis sem dormir.
7. **Concorrência provada por teste:** toda estrutura compartilhada do núcleo (`Gate`, `Lease`, `DegradeService`, `CommitPump`) tem teste de concorrência rodando com `go test -race` no CI.

## 2. Escolha das bibliotecas (com os tradeoffs)

A pedida foi a biblioteca mais performática. Antes da tabela, uma ressalva honesta: **o gargalo é o fsync ([fsync, 04_wal_e_fsync 6.8](../01_arquitetura/04_wal_e_fsync.md)), não HTTP, log ou env.** A 3000 TPS, qualquer uma das opções abaixo cabe no orçamento de CPU. Por isso a regra foi: escolher a mais rápida **que não crie risco operacional**, e deixar cada uma atrás de uma porta para trocar se a medição pedir.

| Papel | Escolha | Alternativas | Por quê / risco |
|---|---|---|---|
| **Injeção de dependência** | `go.uber.org/fx` (decisão do Bruno) | `wire` (geração em compilação) | Fx usa reflexão **só na partida**, custo zero no caminho quente. Riscos: erros de grafo só aparecem na execução (mitigar com `fx.ValidateApp` num teste) e a tentação de espalhar `fx` pelo código. Regra: **só `internal/bootstrap` e `cmd/` importam `fx`** |
| **HTTP da API** (compatível com DynamoDB) | `github.com/valyala/fasthttp` atrás do adaptador `dynamohttp` | `net/http`; `fiber` (é fasthttp com framework por cima, sem ganho aqui) | Menos alocação e menos GC que `net/http`. Serve porque o SDK da AWS fala HTTP/1.1 e a API tem poucas rotas. O mTLS por aplicação ([ADR-052](../03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md)) usa o TLS do `fasthttp` com certificado de cliente exigido; a identidade extraída do certificado é conferida contra a lista de tabelas e operações antes do `Write`, e entra no S7. **Riscos reais:** o corpo da requisição é **reutilizado** após o handler (copiar o que for ao log); não suporta HTTP/2; semântica diferente da biblioteca padrão em casos de borda (`Expect: 100-continue`, chunked, timeouts). Mitigação: suíte de compatibilidade com os SDKs Java, Go e .NET (X3) e **benchmark `net/http` x `fasthttp` no spike S1** antes de congelar |
| **HTTP de admin** (probes, métricas) | `net/http` padrão, porta separada | — | Tráfego irrisório; usar o padrão evita um segundo conjunto de quirks. O Bruno já definiu uma porta de **API**; a de admin é outra, só interna |
| **gRPC entre células** | `google.golang.org/grpc` + `vtprotobuf` | `connectrpc` | Única opção madura com streaming bidirecional e keepalive controlável. `vtprotobuf` gera marshal/unmarshal sem reflexão e com pool. Sem compressão (decisão 4) |
| **Logger** | `github.com/rs/zerolog` | `zap`; `log/slog` (padrão) | Zerolog é JSON com zero alocação e API simples; `zap` empata em velocidade com API maior; `slog` não tem dependência, mas aloca mais. Qualquer um serve se **o caminho quente não loga** (só contadores e histogramas); log de erro com amostragem |
| **Variáveis de ambiente** | Ver nota abaixo | `caarlos0/env`, `koanf` | Ler env acontece **uma vez na partida**: desempenho é irrelevante. O que importa é validar tudo na partida e falhar rápido. `DEGRADED_POLICY` é obrigatória e sem padrão; `DEGRADED_LEASE` não tem padrão e é obrigatória só com `accept`; não há variável de modo: `READ_MODE_DEFAULT`, `WRITE_MODE` ou `MAX_STALENESS` ainda definidas falham na partida ([ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md)) |
| **JSON do DynamoDB** | A decidir no H-DR6 | `encoding/json` (v2 se estável no Go 1.27), `bytedance/sonic`, `goccy/go-json` | É o único item de biblioteca com ganho mensurável. Fica atrás da interface `codec`. Decidir com benchmark sobre itens reais dos clientes, incluindo ARM se usarem Graviton (o `sonic` tem restrições de arquitetura) |
| **Métricas** | `prometheus/client_golang` ou OpenTelemetry | — | Contadores atômicos no caminho quente; exportação fora dele |
| **Raft** | `go.etcd.io/raft/v3` | `dragonboat` (plano B) | Já decidido; importado **só** em `adapter/out/etcdraft` |
| **Log do Raft** | pacote `wal` do etcd (`go.etcd.io/etcd/server/v3/storage/wal`), um por shard | WAL próprio segmentado (W2, desenho anterior) | Decidido em 13/10 ([ADR-055](../03_adr/ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)). Muito testado; custa um fsync por shard e traz o módulo do servidor etcd e o `zap` como dependência. Importado **só** em `adapter/out/etcdraft`. Group commit por shard pelo escritor `adapter/out/raftstore` (só biblioteca padrão), modo síncrono com F3 e nada sai antes do fsync ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)) |
| **Log de replicação** | Pacote próprio `adapter/out/replog` (só biblioteca padrão) | `cockroachdb/pebble` (descartado em 13/10) | Decidido em 13/10 ([ADR-055](../03_adr/ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)): log segmentado, sem fsync por entrada, checkpoint com fsync; volume próprio. Fora do caminho do ACK e refeito a partir do Raft, por isso código próprio é aceitável |
| **Config YAML** | `spf13/viper` (decisão do Bruno) | `koanf` | Ver nota |

> **Nota sobre config e env.** Você decidiu `viper` para o YAML e variáveis de ambiente obrigatórias. O `viper` também lê env, então **uma lib basta**. Minha crítica: `viper` é pesado (muitas dependências, binário maior, e historicamente aceita chaves desconhecidas sem reclamar, o que você já proibiu). Dá para usá-lo com `UnmarshalExact`, e a medição do peso dele já está prevista nos spikes. Se pesar, o `koanf` é o plano B e a troca é barata porque a lib fica atrás de `platform/config`. **Recomendo manter `viper` até a medição.**

## 3. Estrutura de pastas

```text
datareplication/
├── cmd/
│   ├── datareplication/main.go        # só chama bootstrap.New().Run()
│   └── drctl/main.go                  # CLI operacional (rebuild, transfer, inspect)
├── internal/
│   ├── domain/                        # regras puras: sem I/O, sem libs externas
│   │   ├── entry.go                   # Entry, Op, Index, Term, EntryKind
│   │   ├── gate.go                    # Gate: min(Raft, Azure) monotônico, só adota o termo atual (ADR-046)
│   │   ├── lease.go                   # Lease do modo degradado (t_c + D), Clock injetável, DegradedPolicy (ADR-044, ADR-045)
│   │   ├── mode.go                    # ClusterMode (modo único strong, ADR-057: sem ReadMode nem WriteMode)
│   │   ├── tablemap.go                # nome lógico {cell} <-> nome físico
│   │   └── errors.go                  # erros de negócio
│   ├── port/                          # contratos (interfaces)
│   │   ├── in.go                      # Writer, Reader, Admin  (driving)
│   │   └── out.go                     # ReplicationLog, ConsensusEngine, StateStore,
│   │                                  # Forwarder, DedupeIndex, Clock, Metrics (driven)
│   ├── usecase/                       # um arquivo por intenção de negócio
│   │   ├── write.go                   # escrever: líder (WAIT_LEADER), dedupe por requisição, propor, gate, apply, lease, ACK
│   │   ├── batchwrite.go              # lote vira uma Entry; dedupe por requisição inteira (ADR-035)
│   │   ├── read.go                    # leitura forte (barreira min(ReadIndex, gate), ADR-049) só na tabela base; Query em GSI recusada (ADR-050)
│   │   ├── replicate.go               # CommitPump: progresso dos learners -> gate (por evento); commit do Raft -> gate, applier e log de replicação
│   │   ├── checkpoint.go              # fsync do log de replicação, compacta o Raft (RaftCompactLimit) e trunca o log (ReplogTruncateLimit) (ADR-055)
│   │   ├── apply.go                   # aplicar no banco, serial depois do gate, ordem por chave, checkpoint por item (índice, ordem), SKIP por item (ADR-048)
│   │   ├── degrade.go                 # DegradeService: política, DEGRADE/RESTORE/RENEW e lease (ADR-044, ADR-045)
│   │   ├── leadership.go              # transferência planejada (ADR-013)
│   │   └── recovery.go                # partida: completar o log, reabrir leitura
│   ├── adapter/
│   │   ├── in/
│   │   │   ├── dynamohttp/            # API HTTP compatível com DynamoDB (fasthttp); mTLS e lista identidade -> tabelas (ADR-052)
│   │   │   ├── peergrpc/              # servidor gRPC entre células
│   │   │   └── adminhttp/             # /livez /readyz /metrics (net/http)
│   │   └── out/
│   │       ├── etcdraft/              # ÚNICO pacote que importa go.etcd.io/raft e o wal do etcd; LOG DO RAFT (volume do Raft)
│   │       ├── raftstore/             # escritor do log do Raft: group commit por shard, respostas só depois do fsync (ADR-056)
│   │       ├── replog/                # LOG DE REPLICAÇÃO (ReplicationLog) segmentado, sem fsync + checkpoint (volume próprio)
│   │       ├── dynamodb/              # StateStore da AWS (aws-sdk-go-v2); também a tabela sombra do rebuild (ADR-047)
│   │       ├── cosmos/                # StateStore da Azure via Cloud-Adapter
│   │       ├── peerclient/            # cliente gRPC (forward, replicação)
│   │       └── discovery/             # DNS da Route53 privada
│   ├── platform/                      # utilitários transversais, sem regra de negócio
│   │   ├── config/                    # viper, validação estrita
│   │   ├── logx/                      # zerolog
│   │   └── metrics/
│   └── bootstrap/                     # ÚNICO lugar que importa fx
│       ├── app.go                     # monta tudo
│       └── modules.go                 # um fx.Option por adaptador
├── api/proto/                         # .proto do gRPC interno (versionado)
├── test/
│   ├── conformance/                   # suítes por porta (ReplicationLog, ConsensusEngine, StateStore) e a suíte DynamoDB real x camada x Cosmos (ADR-051)
│   ├── fault/                         # injeção de falha (disco, rede, relógio)
│   └── tla/                           # modelo TLA+ ampliado (D12, ADR-046): lease, promoção, troca de membros, dedupe, DELETE e replay
└── .golangci.yml                      # depguard: impõe a regra de dependência
```

**Por que grita:** em `usecase/` você lê `write`, `read`, `apply`, `degrade`, `leadership`, `recovery`: é o que o produto faz. Em `adapter/out/` você lê `etcdraft`, `replog`, `dynamodb`, `cosmos`: são as tecnologias, empurradas para a borda.

### 3.1 Mapa para os módulos do documento base (3.2)

| Módulo do doc base | Onde vive agora |
|---|---|
| `api` | `adapter/in/dynamohttp` + parse/validação em `usecase/write.go` |
| `core` | `domain` + `usecase` + `port` |
| `replicationlog` | `adapter/out/replog` |
| `consensus/etcdraft` | `adapter/out/etcdraft` |
| `transport` | `adapter/in/peergrpc` + `adapter/out/peerclient` + `adapter/out/discovery` |
| `applier` | `usecase/apply.go` (regra) + `adapter/out/dynamodb` e `cosmos` (I/O) |
| `platform` | `platform` + `bootstrap` |

### 3.2 Regras de dependência (impostas por lint)

```yaml
# .golangci.yml (trecho)
linters-settings:
  depguard:
    rules:
      domain:
        files: ["**/internal/domain/**"]
        allow: ["$gostd"]                       # domínio só usa a biblioteca padrão
      usecase:
        files: ["**/internal/usecase/**"]
        allow: ["$gostd", "example.com/datareplication/internal/domain", "example.com/datareplication/internal/port"]
      raft-only-in-adapter:
        files: ["!**/internal/adapter/out/etcdraft/**"]
        deny: [{pkg: "go.etcd.io/raft", desc: "só o adaptador pode importar o Raft"},
               {pkg: "go.etcd.io/etcd/server/v3", desc: "o wal do etcd só no adaptador do Raft"}]
      fx-only-in-bootstrap:
        files: ["!**/internal/bootstrap/**", "!**/cmd/**"]
        deny: [{pkg: "go.uber.org/fx", desc: "DI só no bootstrap"}]
```

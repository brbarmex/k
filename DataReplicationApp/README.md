# DataReplication: POC local com três células AWS

Prova, numa máquina com Docker, o conceito central do DataReplication: uma escrita feita em qualquer célula só é confirmada depois de commitada pelo Raft (2 de 3 com fsync), liberada pelo gate e aplicada no banco da célula líder; em seguida as três células aplicam o mesmo item, com a mesma versão (`_dr_v` = índice do log), cada uma na sua tabela. A leitura forte em qualquer célula devolve o item.

Especificação: [`spec/SPEC_POC_LOCAL.md`](../spec/SPEC_POC_LOCAL.md). Prompt SPDD que gerou este código: [`spdd/prompt/`](../spdd/prompt/).

Passo a passo completo, com os cenários de falha e problemas comuns: [HOW_TO_RUN.md](HOW_TO_RUN.md).

## Pré-requisitos

- Docker com Compose v2
- [Task](https://taskfile.dev) v3
- AWS CLI v2

Não é preciso Go no host: o build e os testes rodam em container.

## Como rodar

```bash
task subir          # compose + tabelas + espera as três células ficarem prontas
task escrever       # escreve um cliente na célula 1 e imprime pk e índice
task buscar         # scan via DR em cada célula (sem _dr_v)
task ler PK=cli-... # get-item via DR em cada célula
task inspecionar    # scan direto no LocalStack (com _dr_v)
task e2e            # tudo acima, falhando com mensagem clara se algo não bate
task limpar         # derruba e apaga os volumes
```

| Tarefa | O que faz | O que observar |
|---|---|---|
| `subir` | `docker compose up -d --build`, `criar-tabelas`, espera o `/readyz` das três | três "cell-N pronta" em até 60 s |
| `criar-tabelas` | cria `cadastro_cliente_cell_{1,2,3}` se não existirem | rodar de novo não dá erro (CA-02) |
| `escrever [CELL=N]` | `put-item` no DR da célula N, na tabela da célula N | `pk=cli-… indice=N` |
| `buscar` | `scan --consistent-read` no DR de cada célula | o mesmo item nas três, sem `_dr_v` |
| `ler PK=…` | `get-item` no DR de cada célula | o mesmo item nas três, sem `_dr_v` |
| `inspecionar` | `scan` direto no LocalStack | o mesmo `_dr_v` nas três tabelas (pode levar até 2 s, AP-09) |
| `status` | `/status` das três células | uma líder, mesmo `lider` e `termo`, `azure: "off"` |
| `wal [CELL=N]` | lista os dois volumes da célula | `.wal` em `/var/lib/dr/raft/wal`; `.seg` e `progress` em `/var/lib/dr/replog` |
| `logs` | `docker compose logs -f` | mudança de líder, encaminhamento, gate parado |
| `parar` / `limpar` | `down` / `down -v` | `limpar` apaga os seis volumes |
| `testar` | `docker build --target test .` (`go test -race ./...`) | CA-14 |
| `e2e` | `subir`, escrever, buscar, ler, inspecionar | `e2e: OK` |

Portas no host: API da célula N em `800N`, administração em `909N`, LocalStack em `4566`. O gRPC entre células (7000) não é publicado.

## Roteiros manuais

- **CA-04, escrita numa follower.** Veja a líder em `task status` e rode `task escrever CELL=<outra>`. O resultado é igual ao da CA-03, e o log da follower registra `escrita encaminhada à líder`.
- **CA-07, sem quórum.** Com a líder na célula 1: `docker compose stop cell-2 cell-3`, **espere 3 s** (o `CheckQuorum` derruba a liderança isolada), depois `task escrever CELL=1`. A resposta é 503 com `x-dr-code: NO_LEADER`, o `/status` da célula 1 mostra `commit` e `gate` parados, e nenhuma tabela recebe o item. `docker compose start cell-2 cell-3` e uma nova escrita funciona.
  Uma escrita que recebe `PENDING` (enviada antes de a liderança cair) é "resultado desconhecido": a entrada pode ser commitada e aplicada depois que o quórum volta. Isso é esperado, não é defeito.
- **CA-08, reinício de follower.** `docker compose restart cell-N` depois de algumas escritas. A célula volta ao `/readyz`, alcança o `aplicado` da líder e o `_dr_v` dos itens não muda (replay idempotente).
- **CA-09, queda da líder.** `docker compose stop cell-<líder>`. Em até 5 s outra célula vira líder e `task escrever CELL=<restante>` funciona. Ao religar, a antiga líder alcança as escritas que perdeu.
- **CA-10, recusas na borda.** `put-item` com `_dr_v`, sem `pk`, com `pk` numérica, ou na `cell-2` com a tabela `cadastro_cliente_cell_1`: 400 (`ValidationException` ou `ResourceNotFoundException`) e o `commit` não muda.
- **CA-12, membros diferentes.** Mude `PEERS` ou `LEARNERS` no compose e suba sem limpar: a célula não sobe e o log diz `PEERS/LEARNERS diferem do que está gravado no WAL …; rode task limpar`.
- **CA-13, fase 2.** `delete-item` ou `batch-write-item`: 400 `ValidationException` "operação não suportada nesta fase (fase 2)".

## Decisões da POC

- **Modo sem Azure (G-08).** Com `LEARNERS` vazia o gate é o commit do Raft e está sempre pronto: RPO 0 só entre as células AWS. A partida só aceita isso com `POC_SEM_AZURE=true`; sem a variável, `LEARNERS` vazia derruba a partida (SG-12). As regras da Azure (G-01 a G-07) são provadas por testes de unidade com learners simulados (`internal/domain/gate_rules_test.go`).
- **Laço do Ready síncrono.** O `etcdraft` segue a RT-04 literalmente (só o `MsgApp` da líder sai antes do `wal.Save`; o resto depois do fsync). O escritor `internal/adapter/out/raftstore` do esboço foi copiado sem mudanças e não é ligado.
- **gRPC sem `.proto`.** `api/peerv1` declara o serviço à mão com um codec JSON (N-03).
- **Applier estacionado.** Erro permanente do banco para o applier daquela célula sem pular o item e sem derrubar o processo; o `/readyz` cai e o `/status` mostra `erroApply`. Erro transitório é repetido com backoff de 100 ms a 5 s.
- **`x-dr-index`.** O AWS CLI não imprime cabeçalhos de resposta; `task escrever` lê o índice da saída de `--debug`.
- **Testes de integração.** O teste do `StateStore` contra o LocalStack só roda com `DR_IT_DYNAMODB_ENDPOINT` definida (ex.: `http://localhost:4566`).

## Bibliotecas

As da N-01: `fasthttp`, `net/http`, `zerolog`, `viper`, `fx`, `google.golang.org/grpc` (v1.84.0), `go.etcd.io/raft/v3` (v3.7.0), `go.etcd.io/etcd/server/v3` (v3.7.2, só o `wal`) e `aws-sdk-go-v2` (`config`, `dynamodb`). Também:

- `github.com/aws/smithy-go`: classifica os erros do SDK em transitório e permanente.
- `go.uber.org/zap`: logger exigido pela API do `wal` do etcd (usado como `zap.NewNop()`).
- `google.golang.org/protobuf`: serializa as mensagens do Raft (o `raftpb` da v3.7 usa protobuf-go).

## Riscos conhecidos

1. Sem Azure, o gate de ponta a ponta com learners não é exercitado; só os testes de unidade cobrem.
2. O modo sem Azure não pode escapar para produção (SG-12).
3. Applier lento ou estacionado enche a fila e pode segurar o laço do Ready.
4. Sem dedupe de retry: uma escrita `PENDING` repetida vira outra entrada (as tasks usam `AWS_MAX_ATTEMPTS=1`).
5. Um único LocalStack para as três células: sem isolamento de falha entre bancos.
6. `inspecionar` logo depois do ACK pode mostrar o item em só 1 ou 2 tabelas.
7. Sem compactação do Raft, a memória cresce com o uso.
8. Uma follower só aplica até o último gate recebido (atraso máximo de `GATE_PUBLISH_INTERVAL`).

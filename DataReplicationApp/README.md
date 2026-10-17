# DataReplication: POC local com três células AWS

Prova, numa máquina com Docker, o conceito central do DataReplication: uma escrita feita em qualquer célula só é confirmada depois de commitada pelo Raft (2 de 3 com fsync), liberada pelo gate e aplicada no banco da célula líder; em seguida as três células aplicam o mesmo item, com a mesma versão (`_dr_v` = índice do log), cada uma na sua tabela. A leitura forte em qualquer célula devolve o item.

Especificação: [`spec/SPEC_POC_LOCAL.md`](../spec/SPEC_POC_LOCAL.md). Prompt SPDD que gerou este código: [`spdd/prompt/`](../spdd/prompt/).

Passo a passo completo, com os cenários de falha e problemas comuns: [HOW_TO_RUN.md](HOW_TO_RUN.md).

## Pré-requisitos

- Docker com Compose v2
- [Task](https://taskfile.dev) v3
- AWS CLI v2
- k6 (para `task k6-load` e `task k6-put-get-load`)

Não é preciso Go no host: o build e os testes rodam em container.

## Como rodar

```bash
task start                    # starts Compose, creates tables, waits for all cells
task write                    # writes through cell 1 and prints the PK and index
task scan                     # scans each cell through the DR API (without _dr_v)
task read PK=cli-...          # gets an item through every cell
task inspect                  # scans LocalStack directly (including _dr_v)
task e2e                      # verifies writes, reads, and replication end to end
task load                     # continuous load for metrics and dashboard
task load COUNT=20            # writes 20 items, then exits
task load INTERVAL=0.5        # waits 500 ms between writes
task k6-load                  # 3-minute PutItem load directly against the leader
task k6-put-get-load          # 3-minute PutItem/GetItem load directly against the leader
task dashboard                # opens Grafana
task clean                    # stops Compose and removes the named volumes
task test-follower-forwarding # checks follower forwarding (CA-04)
task test-follower-restart    # checks follower recovery (CA-08)
task test-follower-outage     # 10-second follower outage, 10 writes, and recovery
task test-leader-failover     # checks leader failover (CA-09)
task test-leader-outage-load  # writes across a 15-second leader outage
task test-no-quorum           # checks writes without quorum (CA-07)
task test-invalid-requests    # checks invalid/unsupported operations (CA-10/13)
```

| Task | What it does | What to check |
|---|---|---|
| `start` | `docker compose up -d --build`, `create-tables`, waits for all `/readyz` endpoints | all cells ready within 60 seconds |
| `create-tables` | creates `cadastro_cliente_cell_{1,2,3}` if missing | safe to run repeatedly (CA-02) |
| `write [CELL=N]` | sends `put-item` through cell N | `pk=cli-… index=N` |
| `scan` | runs `scan --consistent-read` through each cell | same item from all cells, without `_dr_v` |
| `read PK=…` | runs `get-item` through each cell | same item from all cells, without `_dr_v` |
| `inspect` | scans LocalStack directly | same `_dr_v` in all three tables (may take up to 2 s, AP-09) |
| `status` | requests `/status` from all cells | one leader, same `lider` and `termo`, `azure: "off"` |
| `k6-load` | sends PutItem to the current leader: 10 TPS for 1 min, ramps from 10 to 50 TPS over 1 min, then sustains 100 TPS for 1 min | k6 checks each write returned HTTP 200 |
| `k6-put-get-load` | sends a PutItem followed by a strongly consistent GetItem for that same item to the current leader at 10, ramping to 50, then 100 pairs/s | k6 checks both HTTP responses and that GetItem returns the written key |
| `wal [CELL=N]` | lists the cell's Raft volume | `.wal` in `/var/lib/dr/raft/wal`; `progress` in `/var/lib/dr/raft` |
| `logs` | runs `docker compose logs -f` | leader changes, forwarding, stalled gate |
| `stop` / `clean` | runs `down` / `down -v` | `clean` removes all three volumes |
| `test` | runs `docker build --target test .` (`go test -race ./...`) | CA-14 |
| `e2e` | starts the stack, writes, scans, reads, and inspects | `e2e: PASS` |

Portas no host: API da célula N em `800N`, administração em `909N`, LocalStack da célula N em `4565+N` (um por célula: 4566, 4567, 4568), Prometheus em `9095` e Grafana em `3000`. O gRPC entre células (7000) não é publicado.

## Observabilidade

`task start` starts Prometheus and Grafana along with the three cells. Prometheus scrapes `/metrics` from each cell every second; Grafana automatically provisions the datasource and **DataReplication** dashboard. Run `task dashboard` or open [http://localhost:3000/d/datareplication](http://localhost:3000/d/datareplication).

To populate rate and latency panels, run `task load` in another terminal. It writes continuously, alternating across the three cells. Use `task load COUNT=20` to limit the run to 20 attempts or `task load INTERVAL=0.5` to set the delay between writes. Keys use `cli-<startup nanoseconds + write number>` to avoid collisions within one run.

O dashboard reúne:

- **Cluster e liderança:** líder conhecida, termo, votantes ativos, quórum, células prontas e papel de cada célula.
- **Escritas e leituras:** ACKs por resultado, latência, encaminhamentos e barreiras de leitura forte.
- **Replicação e Raft:** índices de commit, gate e apply, atraso entre células, mensagens/heartbeats, fsync do WAL e saúde do log.
- **08 · Entrada HTTP e contrapressão (ADR-060):** TPS de entrada por célula e por operação, respostas por código HTTP, latência de todas as respostas, requisições em andamento e contrapressão por causa.
- **09 · Apply e laço do Raft (ADR-060):** atraso do apply contra o teto `APPLY_LAG_MAX`, commit ainda não entregue ao applier, fila do applier, vazão e latência do apply, duração de cada `Ready` e intervalo real entre ticks.

O painel "Resultados de escrita por segundo" não empilha mais as séries: empilhado, a linha de cima era a soma de todos os resultados e parecia ser só throttling.

State panels are useful even without load; run `task load` to see write activity.

O adaptador `prometheus/client_golang` fica isolado em `internal/adapter/out/prommetrics`: ele fornece o registro, os tipos de métricas e o handler HTTP compatíveis com o Prometheus, sem acoplar o domínio ou os casos de uso à biblioteca. As métricas de evento são contadas/medidas quando ocorrem; as de estado são coletadas do mesmo retrato usado pelo `/status`. Todas incluem o rótulo `celula`; as séries marcadas com rótulos adicionais permitem detalhar por tipo, resultado, operação ou par.

| Série | Tipo / rótulos adicionais | Significado |
|---|---|---|
| `dr_leader_changes_total` | contador | Mudanças de líder observadas pela célula, inclusive para sem líder. |
| `dr_raft_messages_sent_total` | contador, `tipo` | Mensagens Raft enviadas, por tipo. |
| `dr_raft_messages_received_total` | contador, `tipo` | Mensagens Raft recebidas, por tipo. |
| `dr_raft_last_heartbeat_timestamp_seconds` | gauge | Instante Unix do último heartbeat ou resposta recebido. |
| `dr_wal_fsync_duration_seconds` | histograma | Duração da gravação/fsync do WAL. |
| `dr_peer_send_dropped_total` | contador, `par` | Mensagens Raft descartadas por fila cheia, por destino. |
| `dr_forwards_total` | contador | Escritas encaminhadas da follower para a líder. |
| `dr_writes_total` | contador, `resultado` | Escritas recebidas, classificadas por resultado. |
| `dr_write_duration_seconds` | histograma | Latência das escritas confirmadas, da chegada até a resposta. |
| `dr_reads_total` | contador, `op`, `resultado` | Leituras fortes, por operação e resultado. |
| `dr_read_barrier_duration_seconds` | histograma | Duração da barreira de leitura forte e espera do apply local. |
| `dr_is_leader` | gauge | `1` se a célula é líder; `0` se follower. |
| `dr_leader_id` | gauge | ID da líder conhecida; `0` quando não há líder conhecida. |
| `dr_raft_term` | gauge | Termo Raft atual observado pela célula. |
| `dr_commit_index` | gauge | Último índice commitado pelo Raft. |
| `dr_gate_index` | gauge | Índice liberado pelo gate para ACK e apply. |
| `dr_gate_ready` | gauge | `1` se o gate está pronto para leitura forte. |
| `dr_applied_index` | gauge | Marca d'água do apply: o maior índice que, com todos os anteriores, está no banco local (ADR-058). |
| `dr_quorum_active` | gauge | Votantes ativos recentes, incluindo a líder; só na líder. |
| `dr_quorum_voters` | gauge | Número de votantes configurados; só na líder. |
| `dr_quorum_ok` | gauge | `1` se a líder vê a maioria ativa; só na líder. |
| `dr_peer_match_index` | gauge, `par` | Último índice confirmado por cada par; só na líder. |
| `dr_peer_lag` | gauge, `par` | Quantas entradas cada par está atrás do commit da líder; só na líder. |
| `dr_applier_stopped` | gauge | `1` se o applier parou numa entrada que não conseguiu aplicar. |
| `dr_ready` | gauge | `1` se a célula responde `/readyz` como pronta. |
| `dr_azure_off` | gauge | `1` no modo sem Azure desta POC. |
| `dr_apply_workers` | gauge | Workers do applier: itens gravados em paralelo, por chave (`APPLY_WORKERS`). |
| `dr_apply_inflight` | gauge | Itens despachados ao banco e ainda não concluídos pelo applier. |
| `dr_apply_item_duration_seconds` | histograma | Duração da gravação de um item no banco pelo applier, com as repetições. |
| `dr_http_requests_total` | contador, `op`, `codigo` | Toda requisição da porta de API, por operação e código HTTP: é o TPS de entrada por célula (ADR-060). Operação desconhecida conta como `outra`. |
| `dr_http_request_duration_seconds` | histograma, `op` | Latência de todas as respostas da porta de API, inclusive recusas e erros. |
| `dr_http_inflight` | gauge | Requisições em andamento na porta de API. |
| `dr_throttled_total` | contador, `causa` | Escritas recusadas com `ThrottlingException`, pela causa: `buffer_cheio`, `nao_lider`, `transferencia` ou `atraso_apply`, na célula onde a causa nasceu. |
| `dr_raft_loop_duration_seconds` | histograma | Duração de cada `Ready` do laço do Raft (inclui o fsync). |
| `dr_raft_tick_gap_seconds` | histograma | Intervalo real entre dois ticks. Perto de `ELECTION_TICKS × 100 ms`, a célula some para as outras. |
| `dr_apply_lag` | gauge | Gate menos aplicado: o que a admissão compara com `APPLY_LAG_MAX`. |
| `dr_apply_lag_max` | gauge | Teto da admissão (`APPLY_LAG_MAX`). |
| `dr_apply_inbox_batches` | gauge | Lotes na fila entre o `CommitPump` e o applier (teto 64). |
| `dr_commit_undelivered` | gauge | Commit menos o último índice que o `CommitPump` entregou: entradas que esperam na memória do Raft. |
| `dr_election_ticks` | gauge | `ELECTION_TICKS` em uso. |

## Roteiros manuais

- **CA-04, follower forwarding.** Run `task test-follower-forwarding`; it detects the current leader, writes through a follower, and confirms success.
- **Follower outage and recovery.** Run `task test-follower-outage`; it reports status before, during, and after a 10-second follower outage while 10 writes go to the leader, then confirms that the restarted follower catches up.
- **CA-07, loss of quorum.** Run `task test-no-quorum`; it stops both followers, confirms the write is rejected, then restores them.
  Uma escrita que recebe `PENDING` (enviada antes de a liderança cair) é "resultado desconhecido": a entrada pode ser commitada e aplicada depois que o quórum volta. Isso é esperado, não é defeito.
- **CA-08, reinício de follower.** `docker compose restart cell-N` depois de algumas escritas. A célula volta ao `/readyz`, alcança o `aplicado` da líder e o `_dr_v` dos itens não muda (replay idempotente).
- **CA-09, leader failover.** Run `task test-leader-failover`; it stops the current leader, writes through the replacement, restores the old leader, and waits for it to catch up.
- **Leader outage with writes.** Run `task test-leader-outage-load`; it writes through the current leader for five seconds, stops it for at least 15 seconds, sends ten writes through the replacement leader over ten seconds, then verifies the old leader has caught up.
- **CA-10/13, rejected requests.** Run `task test-invalid-requests`; invalid and unsupported operations must be rejected without advancing the Raft commit index.
- **CA-10, recusas na borda.** `put-item` com `_dr_v`, sem `pk`, com `pk` numérica, ou na `cell-2` com a tabela `cadastro_cliente_cell_1`: 400 (`ValidationException` ou `ResourceNotFoundException`) e o `commit` não muda.
- **CA-12, mismatched membership.** Change `PEERS` or `LEARNERS` in Compose and restart without cleaning; the cell must reject the configuration mismatch. Restore the original Compose file afterward.
- **CA-13, fase 2.** `delete-item` ou `batch-write-item`: 400 `ValidationException` "operação não suportada nesta fase (fase 2)".

## Decisões da POC

- **Modo sem Azure (G-08).** Com `LEARNERS` vazia o gate é o commit do Raft e está sempre pronto: RPO 0 só entre as células AWS. A partida só aceita isso com `POC_SEM_AZURE=true`; sem a variável, `LEARNERS` vazia derruba a partida (SG-12). As regras da Azure (G-01 a G-07) são provadas por testes de unidade com learners simulados (`internal/domain/gate_rules_test.go`).
- **Laço do Ready síncrono.** O `etcdraft` segue a RT-04 literalmente (só o `MsgApp` da líder sai antes do `wal.Save`; o resto depois do fsync).
- **gRPC sem `.proto`.** `api/peerv1` declara o serviço à mão com um codec JSON (N-03).
- **Apply paralelo por chave (ADR-058).** O applier grava até `APPLY_WORKERS` itens ao mesmo tempo (padrão 16). Cada item vai para o worker escolhido pelo hash de (tabela, chave), então a mesma chave é sempre gravada na ordem do log. O `aplicado` é a marca d'água: o maior índice que, com todos os anteriores, está no banco; é ele que o ACK da líder e a leitura forte esperam, e é ele que vai para o arquivo `progress`. O `/status` mostra `apply.workers` e `apply.emVoo`. `APPLY_WORKERS=1` volta ao comportamento de um item por vez, útil para comparar.
- **O laço do Raft nunca espera o apply (ADR-060).** O laço do `Ready` só publica o índice commitado e chama `Advance`; o `CommitPump` lê as entradas por faixa da memória do Raft quando o applier tem espaço, e o gate segue o commit numa goroutine própria. Na líder, quando `gate − aplicado` passa de `APPLY_LAG_MAX` (padrão `APPLY_WORKERS × 256`), a escrita é recusada com `ThrottlingException` (causa `atraso_apply`) antes do `Propose`. `ELECTION_TICKS` é configurável (30 no compose). Na partida, o nó retoma no progresso do applier (ou em 1, sem progresso) em vez de reaplicar o log inteiro; o replay vem do WAL do Raft.
- **Um LocalStack por célula.** Cada célula grava no seu `localstack-N` (`DYNAMODB_ENDPOINT` por célula), como em produção há um DynamoDB por célula. `task test-banco-lento CELL=N` pausa só o banco da célula N e confere que o termo do Raft não muda (ADR-060, C35).
- **Teste de carga local com TPS baixo.** A POC usa só o LocalStack, sem banco em memória (decisão do Bruno, 14/10): carga local baixa (por exemplo 10 TPS) e vazão medida na AWS. Para exercitar a admissão localmente, `task start APPLY_LAG_MAX=50`.
- **Applier estacionado.** Erro permanente do banco para o applier daquela célula sem pular o item e sem derrubar o processo; o `/readyz` cai e o `/status` mostra `erroApply`. Com workers em paralelo, os itens anteriores ao venenoso terminam, o despacho para, e a marca d'água fica logo antes dele. Erro transitório é repetido com backoff de 100 ms a 5 s.
- **Um volume por célula.** O `CommitPump` tem três goroutines (progresso dos learners, gate e commits) e só entrega as entradas ao applier; o WAL do Raft é a única cópia durável delas. O progresso do applier é um registro de 16 bytes com CRC, gravado sem fsync no arquivo `/var/lib/dr/raft/progress`, ao lado do WAL e no mesmo volume (`cell-N-raft`); perdê-lo só alonga o replay, que é idempotente pela versão. A compactação do Raft fica desligada na POC (RT-11).
- **`x-dr-index`.** AWS CLI does not print response headers; `task write` reads the index from `--debug` output.
- **Testes de integração.** O teste do `StateStore` contra o LocalStack só roda com `DR_IT_DYNAMODB_ENDPOINT` definida (ex.: `http://localhost:4566`).

## Bibliotecas

As da N-01: `fasthttp`, `net/http`, `zerolog`, `viper`, `fx`, `google.golang.org/grpc` (v1.84.0), `go.etcd.io/raft/v3` (v3.7.0), `go.etcd.io/etcd/server/v3` (v3.7.2, só o `wal`) e `aws-sdk-go-v2` (`config`, `dynamodb`). Também:

- `github.com/aws/smithy-go`: classifica os erros do SDK em transitório e permanente.
- `go.uber.org/zap`: logger exigido pela API do `wal` do etcd (usado como `zap.NewNop()`).
- `google.golang.org/protobuf`: serializa as mensagens do Raft (o `raftpb` da v3.7 usa protobuf-go).

## Riscos conhecidos

1. Sem Azure, o gate de ponta a ponta com learners não é exercitado; só os testes de unidade cobrem.
2. O modo sem Azure não pode escapar para produção (SG-12).
3. ~~Applier lento ou estacionado segura o laço do Ready.~~ Fechado pelo ADR-060: aconteceu no teste de carga de 14/10 e gerou eleições em série. Agora o laço não espera o apply; o que sobra é a líder recusar escrita com `atraso_apply` enquanto o banco estiver lento e a memória do Raft crescer com o apply parado (`dr_commit_undelivered`).
4. Sem dedupe de retry: uma escrita `PENDING` repetida vira outra entrada (as tasks usam `AWS_MAX_ATTEMPTS=1`).
5. ~~Um único LocalStack para as três células.~~ Resolvido em 14/10: um LocalStack por célula, como em produção; os três ainda dividem a CPU e o disco do notebook.
6. `inspecionar` logo depois do ACK pode mostrar o item em só 1 ou 2 tabelas.
7. Sem compactação do Raft, a memória cresce com o uso. Em produção, mesmo com a compactação ligada, o `MemoryStorage` guarda em RAM tudo o que não foi compactado e cresce com o atraso da Azure num modo degradado longo, até o teto da janela (mitigações em aberto; medir no spike S1).
8. Uma follower só aplica até o último gate recebido (atraso máximo de `GATE_PUBLISH_INTERVAL`).

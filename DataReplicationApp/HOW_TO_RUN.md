# Como rodar a POC

Passo a passo para subir as três células, escrever, ler e testar os cenários de falha. O que a POC prova e as decisões de desenho estão no [README](README.md).

## 1. Pré-requisitos

| Ferramenta | Versão | Como conferir |
|---|---|---|
| Docker com Compose v2 | Docker Desktop 4.x ou equivalente | `docker compose version` |
| Task | v3 | `task --version` |
| AWS CLI | v2 | `aws --version` |
| curl | qualquer | `curl --version` |

Não é preciso Go no host: o build e os testes rodam em container.

Instalar o Task no macOS:

```bash
brew install go-task
```

Se o build falhar com `docker-credential-desktop: executable file not found in $PATH`, coloque os binários do Docker Desktop no PATH:

```bash
export PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH"
```

Nenhuma credencial real é usada: o taskfile já define `test`/`test` e a região `sa-east-1`.

## 2. Subir e conferir

Todos os comandos rodam dentro desta pasta (`DataReplicationApp/`).

```bash
task clean   # starts from empty Docker volumes
task start   # builds the image, starts Compose, creates tables, waits for all cells
task status  # one leader, same leader and term on all cells, "azure":"off"
```

`task start` takes about 30 seconds with the image already built. The first build takes longer.

| Serviço | Porta no host |
|---|---|
| API (DynamoDB) da célula N | `800N` (8001, 8002, 8003) |
| Administração da célula N (`/livez`, `/readyz`, `/status`) | `909N` (9091, 9092, 9093) |
| LocalStack da célula N (um por célula) | `4565+N` (4566, 4567, 4568) |
| Prometheus | `9095` |
| Grafana | `3000` |

## 3. Usar

```bash
task write                  # writes through cell 1; prints pk and index
task write CELL=2           # writes through cell 2 (forwarded if it is a follower)
task scan                   # scans each cell through the DR API, without _dr_v
task read PK=cli-...        # gets an item through each cell, without _dr_v
task inspect                # scans LocalStack directly, including _dr_v
task wal CELL=1             # lists the Raft WAL and replication log files
task logs                   # follows all container logs
task load                   # continuous load for dashboard activity
task load COUNT=20          # runs 20 writes, then exits
task load INTERVAL=0.5      # waits 500 ms between writes
task dashboard              # opens the Grafana dashboard
```

Immediately after `task write`, `task inspect` may show the item in only one or two tables: only the leader applies it before responding, and followers apply it shortly after (up to 2 s). `task scan` and `task read` use the strong-read barrier.

## 4. Dashboard e observabilidade

`task start` starts Prometheus and Grafana along with the cells. The **DataReplication** dashboard is provisioned automatically:

- Grafana: [http://localhost:3000/d/datareplication](http://localhost:3000/d/datareplication), or `task dashboard`.
- Prometheus: [http://localhost:9095](http://localhost:9095). Under **Status → Targets**, `cell-1`, `cell-2`, and `cell-3` should be `UP`.
- Each cell's metrics: `http://localhost:909N/metrics`.

To view write and read rates, run `task load` in another terminal. It alternates writes across all three cells until Ctrl+C. Use `task load COUNT=20` for a finite run or `task load INTERVAL=0.5` to change the interval. Each key is `cli-<startup nanoseconds + write number>`, unique within that run. Rate panels may show zero or no data when the cluster is idle; state panels remain available.

### O que observar nos cenários de falha

- **Normal operation:** `Current Leader` shows one cell, `Quorum` is `OK`, `Ready Cells` is 3, and `Role per Cell` shows one leader and two followers. With `task load` running, successful ACKs appear and `commit`, `gate`, and `applied` advance on all cells.
- **Leader failure (CA-09):** during election, `Current Leader` may show `NO LEADER` and the stopped cell is no longer ready. After failover, `Leader Changes` increases, `Term` advances, and the new leader appears in `Role per Cell`. Quorum returns to `OK`; writes resume. The old leader rejoins as a follower and catches up.
- **Quorum loss (CA-07):** stop two cells and wait for `CheckQuorum`. The `Quorum` panel shows `NO QUORUM`, active voters drops below 2, and the remaining cell no longer reports a leader. A write attempt is counted as `no_leader` (or `pending` if it was already in flight); indexes stop advancing. Restart the cells and wait for election, quorum, and readiness before checking for successful ACKs.

To exercise these signals, run `task test-leader-failover` or `task test-no-quorum` with the dashboard open. Run `task load` in a separate terminal for live traffic and stop it with Ctrl+C afterward. `PENDING` means the result is unknown, not definitively rejected.

## 4.1 Teste de carga

Num notebook, o teste de carga mede o notebook tanto quanto o DR: os três WALs, os três logs de replicação, o LocalStack, o Prometheus e o gerador de carga dividem o mesmo disco e as mesmas CPUs. Antes de tirar conclusão:

1. **Comece de volumes limpos.** `task clean` e depois `task start`. Volumes de testes anteriores deixam o apply com atraso desde o início (no teste de 14/10 eram cerca de 210 mil entradas).
2. **Meça o disco** no diretório dos volumes do Docker: `fio --rw=write --ioengine=sync --fdatasync=1 --size=22m --bs=2300 --name=wal`. O P99 do fdatasync dá a ordem de grandeza do `ELECTION_TICKS` necessário.
3. **Meça os LocalStacks sozinhos** (um por célula, as três instâncias ao mesmo tempo), com o mesmo `PutItem` condicional do applier:

   ```bash
   task bench-localstack                         # 16 workers por célula, 30 s
   task bench-localstack WORKERS=32 DURATION=60s
   ```

   A vazão impressa é o teto do apply na sua máquina. Se o gerador de carga passar disso, o atraso do apply cresce e a líder passa a recusar com `atraso_apply`. É o comportamento esperado, não defeito. Cada LocalStack sobe com `DYNAMODB_HEAP_SIZE=512m`; mude a variável de ambiente para testar outro valor.
4. **Use TPS baixo localmente** (por exemplo 10 TPS). O LocalStack não aguenta a vazão de produção; a vazão se mede na AWS. Com carga baixa o apply acompanha e não deve haver contrapressão. Para ver a admissão por atraso funcionar sem carga alta, suba com um teto baixo:

   ```bash
   task clean
   task start APPLY_LAG_MAX=50    # a líder recusa com atraso_apply quando o apply fica 50 entradas atrás
   ```

   Para ver o laço do Raft seguir com o banco de uma célula parado (o problema do teste de 14/10), pause só o LocalStack de uma célula:

   ```bash
   task test-banco-lento CELL=2            # pausa o banco da célula 2 por 30 s enquanto escreve pela líder
   task test-banco-lento CELL=1 PAUSE=60   # se a célula 1 for a líder, as escritas falham, mas o termo não pode mudar
   ```

   A task falha se o termo de alguma célula mudar e, no fim, confere que a célula alcançou o gate.

5. **Rode a carga e leia o diagnóstico:**

   ```bash
   task load                 # ou o seu k6
   task diag                 # tabela por célula, janela de 1 min
   task diag WINDOW=5m
   ```

   O `task diag` imprime, por célula: entrada HTTP, ACK, pending, vazão e latência do apply, atraso, commit não entregue, tick e fsync P99, trocas de líder e contrapressão por causa.

**O que olhar no dashboard:** seção **08** (TPS de entrada por célula, códigos HTTP, contrapressão por causa) e seção **09** (atraso do apply contra o teto, commit não entregue, fila do applier, duração do `Ready`, intervalo entre ticks). Sinal de saúde: o termo não muda durante a carga e o intervalo entre ticks fica perto de 100 ms. Se aparecer contrapressão, a causa diz o motivo: `atraso_apply` (banco lento), `buffer_cheio` (Raft sem conseguir commitar), `nao_lider` (logo depois de uma troca de líder) ou `transferencia`.

## 5. Teste automático

```bash
task e2e   # starts the stack and verifies writes, reads, and replication
task test  # runs go test -race in the test container

# Scenario checks
task test-follower-forwarding  # CA-04
task test-follower-restart     # CA-08
task test-follower-outage      # 10-second outage, 10 writes, recovery
task test-leader-failover      # CA-09
task test-leader-outage-load   # leader outage with writes before and after failover
task test-no-quorum            # CA-07
task test-banco-lento CELL=2   # CA-19: banco de uma célula pausado, sem eleição
task test-invalid-requests     # CA-10 and CA-13
```

## 6. Failure scenarios

The automated scenario tasks detect the current leader and restore stopped cells automatically. For manual tests, first check the leader with `task status`.

Os comandos `aws` diretos usam as credenciais de teste. Exporte-as uma vez no terminal:

```bash
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=sa-east-1 AWS_MAX_ATTEMPTS=1
```

### Write through a follower (CA-04)

**What it tests:** a follower does not write locally. It forwards the request to the leader in one hop and returns the leader's result.

```bash
task test-follower-forwarding
```

**Resultado esperado:**
- The task prints `PASS` after a write through a follower succeeds.
- Verify the item with `task scan` and `task inspect`.

### Restart a follower (CA-08)

**What it tests:** a restarted follower recovers from its Raft WAL and applier progress, then catches up with the leader. Replay is idempotent.

```bash
task test-follower-restart
task status
task inspect
```

**Resultado esperado:**
- The restarted follower returns to `/readyz` and catches up to the leader's `commit` and `applied` indexes.
- Use `task inspect` to compare `_dr_v` values across the cell tables.

### Follower outage with writes (10 seconds)

**What it tests:** a follower remains stopped for 10 seconds while the leader accepts 10 new writes, then rejoins and catches up.

```bash
task test-follower-outage
```

The task prints `/status` for all cells before stopping the follower, halfway through the outage, at the end of the outage, and after restart and catch-up. During the outage, the stopped cell is shown as `unavailable`; the leader's `commit` and `aplicado` indexes should advance as the 10 writes succeed. After restart, the follower must return to `/readyz` and its `commit` and `aplicado` indexes must be at least the leader's.

### Leader failover (CA-09)

**What it tests:** after the leader stops, the other two cells retain quorum, elect a new leader, and continue accepting writes. The old leader rejoins as a follower.

```bash
task test-leader-failover
task status
```

**Resultado esperado:**
- Within 5 seconds, another cell becomes leader and the Raft term increases.
- The task writes through the new leader, restores the old leader, and leaves it running.
- Check `task status` to confirm the old leader has rejoined.

### Leader outage while writing

**What it tests:** writes continue across a leader failure: five writes go through the current leader over five seconds, that leader is stopped for at least 15 seconds, and ten writes are sent through the elected leader over ten seconds.

```bash
task test-leader-outage-load
task status
```

The task prints `/status` before the outage, after failover, before restoring the old leader, and after recovery. The old leader stays stopped while the new leader handles writes. At the end, the old leader must return as a follower and catch up to the new leader's `commit` and `aplicado` indexes. In total, the scenario spans at least 20 seconds: five seconds of pre-outage writes plus a 15-second leader outage.

### Quorum loss (CA-07)

**What it tests:** without two of three cells, writes are not confirmed or applied. The client must not receive success without a durable majority.

```bash
task test-no-quorum
task status
```

**Resultado esperado:**
- After 3 seconds, the isolated leader reports no known leader (`CheckQuorum`).
- The write is rejected with `NO_LEADER` and the task restores both followers.
- Check `task status` to confirm the cluster elects a leader and recovers.

If a write is sent **before** leadership is lost (without waiting 3 seconds), the response may be 503 `PENDING`, meaning its outcome is unknown. `PENDING` does not mean the write definitively failed.

### Invalid and unsupported requests (CA-10 and CA-13)

**What it tests:** requests rejected by DynamoDB are rejected **before** reaching Raft. Invalid requests must not become log entries.

```bash
task test-invalid-requests
```

**Expected result:** all five requests are rejected with HTTP 400; the Raft commit index is unchanged.

| Request | Expected error |
|---|---|
| Item com `_dr_v` | `ValidationException`: `o atributo _dr_v é reservado` |
| Item sem `pk` | `ValidationException`: `One or more parameter values were invalid: Missing the key pk in the item` |
| `pk` numérica | `ValidationException`: `One or more parameter values were invalid: Type mismatch for key pk` |
| Tabela da `cell-1` enviada à `cell-2` | `ResourceNotFoundException`: `Requested resource not found` |
| `delete-item` (também `batch-write-item`) | `ValidationException`: `operação não suportada nesta fase (fase 2)` |

### Mismatched membership configuration (CA-12)

**What it tests:** cluster membership is persisted in the WAL. A cell must not start with a different membership configuration.

This scenario intentionally edits the cluster configuration and is not automated. In `docker-compose.yml`, change `PEERS` in `x-dr-env` (for example, append `,4=cell-4:7000`) and run `docker compose up -d`. Do not run it alongside other scenario tests.

**Expected result:**
- Cells using old volumes exit at startup and `/readyz` does not respond.
- `task logs` reports that `PEERS/LEARNERS` differ from the values persisted in the WAL.
- Restore the original Compose configuration and run `docker compose up -d` to recover.

## 7. Stop and clean

```bash
task stop   # docker compose down; keep DR volumes
task clean  # docker compose down -v; remove named volumes
```

**Warning:** LocalStack stores data only in memory. After `task stop` and `task start`, DR volumes return with the log and applier progress, but the tables are empty and the applier does not replay already-applied data. The status may show everything applied while reads return nothing. Use `task clean` before `task start` to begin with coherent data.

## 8. Troubleshooting

| Symptom | Cause | Action |
|---|---|---|
| `docker-credential-desktop: executable file not found` | Docker Desktop binaries are not on `PATH` | See section 1 |
| `start` fails with "not ready within 60 s" | LocalStack is slow or a table is missing | Run `task logs`; look for table readiness and `TABLE_WAIT` |
| Cell exits with "PEERS/LEARNERS differ from WAL" | Configuration changed while old volumes remain | Restore the configuration; only then use `task clean` if resetting data is intended |
| `write` returns `NO_LEADER` | Election in progress or quorum unavailable | Wait 1-2 seconds and check `task status` |
| `/readyz` reports a stopped applier | Database rejected an item after commit (poison entry) | Check `erroApply` in `task status`; recovery requires operator action (ADR-038) |
| Tables are empty after `stop` and `start` | LocalStack is not persistent | Use `task clean` then `task start` to reset coherently |
| Muita `ThrottlingException` com causa `atraso_apply` | O apply não acompanha a carga (banco lento) | Compare com `task bench-localstack`; reduza a carga ou aumente `APPLY_WORKERS`; a vazão real se mede na AWS |
| Trocas de líder durante a carga | Laço do Raft lento: fsync com picos acima do timeout de eleição | Veja `dr_raft_loop_duration_seconds`, `dr_raft_tick_gap_seconds` e o fsync P99 no `task diag`; meça o disco com `fio`; `ELECTION_TICKS` já é 30 no compose |

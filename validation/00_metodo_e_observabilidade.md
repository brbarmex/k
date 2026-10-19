# 00 · Método comum, ambiente EKS, oráculo do RPO e catálogo de métricas

Este arquivo vale para **todos** os cenários desta pasta. Cada cenário cita os itens daqui pelo código (`AMB-`, `CRG-`, `ORC-`, `RTO-`, `L-`, `G-`, `B-`, `M-`) e só detalha o que é próprio dele.

Base conferida no código local de `DataReplicationApp/` (commit `c9f42af`, 18/10/2026), revista para o commit `eec02c9` (compactação do log, Incremento A). Onde algo foi deduzido do código e não observado, o texto diz "inferido".

---

## 1. Ambiente na AWS (AMB)

| Código | Item | Definição para a validação | Por que importa |
|---|---|---|---|
| AMB-01 | Células | 3 clusters EKS, um por AZ (`sa-east-1a/b/c`), mesma VPC. Em cada um, um StatefulSet `datareplication` com `replicas: 1`. | Domínio de falha = AZ. Um cluster único com 3 node groups é mais barato, mas o plano de controle compartilhado muda o cenário de perda de AZ (FP-12). Registrar qual foi usado. |
| AMB-02 | Disco do WAL | PVC próprio, StorageClass `ebs.csi.aws.com`, `type: gp3`, `iops: 3000`, `throughput: 125`, `volumeBindingMode: WaitForFirstConsumer`, `reclaimPolicy: Retain`. Tamanho fixo e registrado, **maior que `RETAIN_MAX_BYTES / RETAIN_DISK_HARD`** (com os padrões, 2 GiB / 0,85: mais de ~2,4 GiB, com folga para os segmentos pré-alocados de `WAL_SEGMENT_BYTES`). | gp3 não tem crédito de burst: o teto é o provisionado. `Retain` evita perder o WAL num `delete pvc` acidental (FD-05). A partida recusa um volume menor que a janela de retenção (FL-06 variante e). |
| AMB-03 | Instâncias | Família Nitro (ex.: m6i/m7i), **mesmo tipo nas 3 AZs**. | `aws:ebs:pause-volume-io` do FIS só funciona em Nitro. Tipos diferentes distorcem a comparação de fsync. |
| AMB-04 | Recursos do pod | `requests == limits` de CPU e memória (QoS Guaranteed). Limite de memória registrado e **acima do orçamento da AP-15**: `RAFT_RAM_MAX` + 32 MiB (não commitadas) + `PEER_QUEUE_BYTES` × 2 pares + 64 MiB (fila do applier) + folga; com os padrões, 1 GiB. `GOMEMLIMIT` em ~80% do limite. | Throttling de CPU atrasa o tick do Raft e provoca eleição falsa (CE-03). Um limite abaixo do orçamento traz o OOMKill de volta (FP-09); um `GOMEMLIMIT` mal escolhido aparece como pausas de GC (CF-06). |
| AMB-05 | Sondas | `readinessProbe` em `/readyz`, `livenessProbe` em `/livez`, `periodSeconds` e `failureThreshold` registrados. `terminationGracePeriodSeconds` ≥ 30 (o app usa `fx.StopTimeout(25s)`). | O período da sonda entra no RTO de roteamento. `/livez` sempre responde `ok` (não detecta laço do Raft preso). |
| AMB-06 | Endereços dos pares | `PEERS` com nomes estáveis (NLB interno por célula ou DNS privado no Route 53), nunca IP de pod. | Pod reiniciado troca de IP. O NLB acrescenta salto e latência: medir o RTT entre células no baseline (CF-01). |
| AMB-07 | Banco | 3 tabelas DynamoDB na mesma região (`cadastro_cliente_cell_{1,2,3}`), modo **on-demand** (ou provisionado com WCU registrado), VPC gateway endpoint, credencial por IRSA ou Pod Identity. | O DynamoDB é **regional**: a perda de uma AZ não derruba a tabela da célula. On-demand evita throttling que não é do teste. |
| AMB-08 | Configuração do app | `ELECTION_TICKS=10` (1 s; o compose local usa 30), `APPLY_WORKERS=16`, `APPLY_LAG_MAX` padrão (4096), `WAIT_LEADER=1s`, `FORWARD_TIMEOUT=3s`, `REQUEST_DEADLINE=7.5s`. Compactação (S.6 da trecho): `CLUSTER_ID` igual nas 3 (obrigatória), `SNAPSHOT_EVERY_ENTRIES=10000`, `SNAPSHOT_EVERY_BYTES=64MiB`, `RAFT_CATCHUP_ENTRIES=5000`, `RAFT_RAM_MAX=256MiB`, `RAFT_COMPACT_INTERVAL=1s`, `RETAIN_MAX_BYTES=2GiB`, `RETAIN_DISK_HARD=0.85`, `RETAIN_DISK_HARD_BYTES` vazio, `WAL_SEGMENT_BYTES=64000000`, `PEER_QUEUE_BYTES=64MiB`, `GOMEMLIMIT` (AMB-04); `REBUILD_RETRY` e `REBUILD_PIN_TTL` só valem no Incremento B. Qualquer mudança é registrada no relatório do cenário. | Cada um desses valores entra direto no RTO medido. `RAFT_RAM_MAX` e `RETAIN_MAX_BYTES` decidem por quanto tempo uma follower fora consegue voltar só pelo `MsgApp` (FP-04, FL-02, FL-03). |
| AMB-09 | Gerador de carga | k6 em nós próprios, **fora** dos nós das células, um gerador por AZ (ou um só numa 4ª subnet, registrado). | Gerador no mesmo nó da célula concorre por CPU e mascara falhas. |
| AMB-10 | Observabilidade | Prometheus por cluster em modo agente com `remote_write` para um Amazon Managed Prometheus (ou um Prometheus central fora das 3 AZs da falha), `scrape_interval: 1s`; Grafana; node-exporter; kube-state-metrics; métricas de volume do kubelet; CloudWatch do EBS e do DynamoDB; logs centralizados (CloudWatch Logs ou Loki). | Num teste de partição, um Prometheus que mora numa das células perde justamente a célula isolada. A coleta não pode estar no mesmo domínio de falha que o teste derruba. |
| AMB-11 | Relógios | Amazon Time Sync (chrony) em todos os nós e no gerador; registrar o desvio (`node_timex_offset_seconds`). | O RTO é decomposto comparando instantes de máquinas diferentes. |
| AMB-12 | Injeção de falha | AWS FIS (ações `aws:eks:pod-delete`, `aws:eks:pod-network-latency`, `aws:eks:pod-network-packet-loss`, `aws:eks:pod-network-blackhole-port`, `aws:eks:pod-cpu-stress`, `aws:eks:pod-memory-stress`, `aws:ebs:pause-volume-io`, `aws:ec2:stop-instances`, `aws:ec2:terminate-instances`, `aws:network:disrupt-connectivity`, `aws:ssm:send-command` com os documentos `AWSFIS-Run-*`, cenário "AZ Availability: Power Interruption"), `kubectl` e `aws ec2`. Chaos Mesh é opcional para o que o FIS não cobre (ex.: erro de I/O por errno). | Cada execução do injetor publica uma **anotação no Grafana** com início e fim (ver RTO-01). |

> **Pré-requisito que não existe hoje:** o repositório não tem manifestos de EKS, Helm, Terraform nem StorageClass. Os cenários assumem que eles serão especificados à parte, com os valores acima.

---

## 2. Perfis de carga (CRG)

| Código | Perfil | Definição |
|---|---|---|
| CRG-0 | Sanidade | 10 TPS de PutItem por 1 min. Só confirma que o ambiente responde. |
| CRG-1 | Baseline | Taxa de chegada constante (`constant-arrival-rate`) de **30% da vazão máxima sustentada** medida em CF-05, PutItem 100%, 1 KiB por item, **uma pk nova por escrita**. Aquecimento de 2 min, medição de 10 min. |
| CRG-2 | Alvo do produto | 3000 TPS (alvo do produto). Só roda se CF-05 mostrar que a POC sustenta; senão, registra-se o teto real e CRG-2 = 80% do teto. |
| CRG-3 | Misto | CRG-1 com 10% de GetItem forte (`ConsistentRead: true`) de pks já confirmadas. |
| CRG-4 | Chave quente | 20% das escritas numa mesma pk (FC-01). |

Regras:

- **Uma pk nova por escrita** em todos os cenários de RPO. Sem isso, uma escrita sobrescrita por outra mais nova não se distingue de uma perdida.
- **Tamanho mínimo de amostra para percentil.** p99.9 só é publicado com pelo menos 10.000 amostras na janela (o ideal é 100.000). Em CRG-1 com 300 TPS, 10 min dão 180.000 amostras. Janelas curtas de falha (ex.: os 2 s de uma eleição) não têm amostras para p99.9: nelas o que vale é o **max** e a contagem de erros.
- Em toda execução com falha: 10 min de CRG antes da injeção, a falha, e 10 min depois do fim da falha.
- Os scripts atuais (`k6/putitem-leader.js`, `k6/put-get-leader.js`) apontam para `localhost` e só para a líder: precisam de parâmetros de endpoint por célula e de saída do ledger (ORC-01).

---

## 3. Oráculo do RPO (ORC)

**Crítica central:** as métricas do próprio app (`commit = gate = aplicado` nas 3 células) **não provam RPO = 0**. Elas mostram que o app acha que está tudo certo; um bug no apply, na versão ou no truncamento do log passaria por elas sem aparecer. RPO = 0 só se prova confrontando **o que o cliente recebeu como confirmado** com **o que está nos bancos**, por um observador independente.

| Código | Peça | Definição |
|---|---|---|
| ORC-01 | Ledger do cliente | Para cada requisição, o gerador grava: pk, hash do valor, célula de entrada, tentativa, instante de envio, instante de resposta, status HTTP, `__type` do erro, `x-dr-code`, `x-dr-index` (quando 200). Saída do k6 em JSON com essas tags, ou um gerador próprio. A pk **não** vai para rótulo do Prometheus (cardinalidade). |
| ORC-02 | Classes de resultado | **CONFIRMADA** = 200. **RECUSADA_SEGURA** = 400 `ThrottlingException`, 400 de validação, 503 `x-dr-code` sem líder antes de propor: nada entrou no log. **INCERTA** = 500 (PENDING), timeout do cliente, conexão cortada, 503 vindo do balanceador (não do app). |
| ORC-03 | Reconciliador | Depois da quiescência (as 3 células com `aplicado ≥ commit da líder` por 60 s), lê **direto** as 3 tabelas (Scan com `ConsistentRead`, incluindo `_dr_v`), sem passar pelo proxy. |
| ORC-04 | Regras | **R1** toda CONFIRMADA existe nas 3 tabelas com o mesmo valor e `_dr_v ≥ x-dr-index`. **R2** nenhuma RECUSADA_SEGURA existe em tabela alguma. **R3** cada INCERTA existe nas 3 ou em nenhuma (as duas respostas são válidas; a divergência não). **R4** as 3 tabelas são iguais (mesmo conjunto de pk, mesmo `_dr_v`, mesmo hash). **R5** nenhum `_dr_v` acima do commit da líder. |
| ORC-05 | Publicação | O reconciliador publica no Prometheus (Pushgateway) as séries `dr_val_confirmadas_total`, `dr_val_confirmadas_ausentes_total{celula}`, `dr_val_recusadas_presentes_total{celula}`, `dr_val_incertas_total{presenca}` (valores 3, 0, divergente), `dr_val_itens_divergentes_total`, `dr_val_execucao_timestamp`. É isso que cumpre a regra "comprovado por observabilidade": a prova do RPO aparece num painel, vinda de quem não é o app. |
| ORC-06 | Leitura linearizável | Para cenários com leitura (CRG-3), o histórico de put/get do ledger passa por um verificador de linearizabilidade (ex.: Porcupine, em Go). Percentis não provam consistência; só o verificador prova. |

**Critério de RPO = 0 em todo cenário:** `dr_val_confirmadas_ausentes_total == 0`, `dr_val_recusadas_presentes_total == 0`, `dr_val_incertas_total{presenca="divergente"} == 0`, `dr_val_itens_divergentes_total == 0`.

**Limite do oráculo:** ele prova que nada confirmado se perdeu **nesta execução**. Não prova que é impossível perder. Para as falhas que só violam a segurança numa ordem rara de eventos (ex.: FD-05, voto duplo), o cenário usa uma evidência indireta descrita nele.

---

## 4. Medição do RTO = Delta-T (RTO)

O RTO não tem número fechado. Os cenários de falha **medem** os tempos abaixo, sempre a partir de eventos com instante gravado.

| Código | Tempo | De → até | Fonte |
|---|---|---|---|
| RTO-01 | T0 | instante da injeção | anotação do injetor no Grafana e log do FIS |
| RTO-02 | T_detecção | T0 → primeira célula sem líder ou com termo novo | `dr_leader_id == 0` ou `changes(dr_raft_term)`; log "mudança de líder" |
| RTO-03 | T_eleição | T0 → nova líder com quórum | `dr_is_leader == 1` e `dr_quorum_ok == 1` numa célula com termo maior |
| RTO-04 | **Delta-T de escrita** | T0 → primeira CONFIRMADA depois da falha, vista pelo cliente | ledger (ORC-01) |
| RTO-05 | T_admissão | nova líder → `dr_apply_lag < dr_apply_lag_max` na nova líder | `dr_apply_lag`, `dr_throttled_total{causa="atraso_apply"}` |
| RTO-06 | T_estável | T0 → p99 de L-ACK de volta a ±20% do baseline por 60 s | k6 |
| RTO-07 | MTTR da célula | T0 → célula afetada pronta e com `aplicado ≥ commit da líder` | `dr_ready`, `dr_applied_index`, `dr_commit_index` |
| RTO-08 | Janela de indisponibilidade | soma dos intervalos sem nenhuma CONFIRMADA por mais de 1 s | ledger |

Referência teórica para a eleição (inferida do etcd raft com `PreVote` e `CheckQuorum`): o timeout aleatório fica entre `ELECTION_TICKS` e `2 × ELECTION_TICKS` ticks de 100 ms. Com `ELECTION_TICKS=10`, a eleição esperada fica entre 1 e 2 s mais os RTTs. Valores muito acima disso apontam para algo a investigar (sonda, DNS, NLB, laço preso).

---

## 5. Catálogo de métricas

### 5.1 Latências (L): todas com p90, p99, p99.9 e max

| Código | Sinal | Fonte | Existe? |
|---|---|---|---|
| L-ACK | Latência da escrita vista pelo cliente, por status | k6 `http_req_duration{op="PutItem"}` com `summaryTrendStats: p(90), p(99), p(99.9), max` | k6 sim; tags por célula a acrescentar |
| L-ACK-S | Latência da escrita confirmada no servidor | `dr_write_duration_seconds` (só `ok`) | sim |
| L-HTTP | Latência de toda resposta na API | `dr_http_request_duration_seconds{op}` | sim (sem rótulo de código) |
| L-FSYNC | wal.Save com fdatasync | `dr_wal_fsync_duration_seconds` | sim |
| L-LOOP | Duração de cada Ready | `dr_raft_loop_duration_seconds` | sim |
| L-TICK | Intervalo real entre ticks | `dr_raft_tick_gap_seconds` | sim |
| L-APPLY | Gravação de um item no DynamoDB pelo applier | `dr_apply_item_duration_seconds` | sim |
| L-READ | Barreira da leitura forte | `dr_read_barrier_duration_seconds`; k6 `http_req_duration{op="GetItem"}` | sim |
| L-FWD | Encaminhamento follower → líder | — | **não** (M-02) |
| L-ETAPAS | propose → commit, commit → gate, gate → apply local | — | **não** (M-01) |
| L-REPL | Atraso de replicação em tempo por follower | — | **não** (M-11); hoje só em entradas (`dr_apply_lag`) |
| L-DB | Latência do DynamoDB no lado da AWS | CloudWatch `SuccessfulRequestLatency` (1 min) | sim, granularidade grossa |
| L-EBS | Latência do disco no SO | node-exporter `node_disk_write_time_seconds_total / node_disk_writes_completed_total`; CloudWatch `VolumeTotalWriteTime` | sim |

**Como obter os quatro números:**

- **p90 / p99 / p99.9 no servidor:** `histogram_quantile(0.90|0.99|0.999, sum by (le, celula) (rate(<métrica>_bucket[1m])))`. Os baldes atuais são exponenciais com fator perto de 2 (ex.: `dr_write_duration_seconds` tem 14 baldes entre 1 ms e 10 s): o p99.9 interpolado pode errar quase por um fator 2. Por isso, ver M-03.
- **max:** histograma clássico do Prometheus **não tem max**. O max confiável vem do k6 (cliente) e, no servidor, de M-03.
- **Fonte da verdade para o requisito:** o cliente (k6). O servidor serve para explicar onde o tempo foi gasto.

### 5.2 Golden signals (G)

| Código | Sinal | Consulta (PromQL) |
|---|---|---|
| G-LAT | Latência | ver 5.1 (L-ACK, L-HTTP) |
| G-TRF-1 | Tráfego de entrada por célula | `sum by (celula, op) (rate(dr_http_requests_total[1m]))` |
| G-TRF-2 | Confirmações por segundo | `sum by (celula) (rate(dr_writes_total{resultado="ok"}[1m]))` |
| G-TRF-3 | Encaminhamentos por segundo | `sum by (celula) (rate(dr_forwards_total[1m]))` |
| G-ERR-1 | Escritas não OK por resultado | `sum by (celula, resultado) (rate(dr_writes_total{resultado!="ok"}[1m]))` |
| G-ERR-2 | Respostas HTTP não 200 | `sum by (celula, op, codigo) (rate(dr_http_requests_total{codigo!="200"}[1m]))` |
| G-ERR-3 | Contrapressão por causa | `sum by (celula, causa) (rate(dr_throttled_total[1m]))` |
| G-ERR-4 | Leituras não OK | `sum by (celula, op, resultado) (rate(dr_reads_total{resultado!="ok"}[1m]))` |
| G-ERR-5 | Erros vistos pelo cliente | k6 `http_req_failed`, por status e célula |
| G-SAT-1 | Requisições em andamento | `dr_http_inflight` |
| G-SAT-2 | Atraso do apply contra o teto | `dr_apply_lag / dr_apply_lag_max` |
| G-SAT-3 | Commit não entregue e fila do applier | `dr_commit_undelivered`, `dr_apply_inbox_batches`, `dr_apply_inflight / dr_apply_workers` |
| G-SAT-4 | CPU e throttling | `rate(container_cpu_usage_seconds_total[1m])`, `rate(container_cpu_cfs_throttled_periods_total[1m]) / rate(container_cpu_cfs_periods_total[1m])` |
| G-SAT-5 | Memória | `process_resident_memory_bytes`, `go_memstats_heap_inuse_bytes`, limite do container |
| G-SAT-6 | Disco | `kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes`; `rate(node_disk_io_time_seconds_total[1m])`; CloudWatch `VolumeQueueLength`, `VolumeIOPSExceededCheck`, `VolumeThroughputExceededCheck` |
| G-SAT-7 | Banco | CloudWatch `ThrottledRequests`, `ConsumedWriteCapacityUnits`, `SystemErrors`, `UserErrors` por tabela |

### 5.3 Além dos golden signals (B)

| Código | Grupo | Sinais |
|---|---|---|
| B-RAFT-1 | Liderança | `dr_raft_term`, `dr_leader_id`, `dr_is_leader`, `increase(dr_leader_changes_total[...])` |
| B-RAFT-2 | Quórum | `dr_quorum_ok`, `dr_quorum_active`, `dr_quorum_voters` (só na líder) |
| B-RAFT-3 | Replicação | `dr_peer_match_index{par}`, `dr_peer_lag{par}` (só na líder) |
| B-RAFT-4 | Mensagens | `rate(dr_raft_messages_sent_total{tipo}[1m])` e `received`, com atenção a `vote`, `prevote`, `snap`; `dr_peer_send_dropped_total{par}` |
| B-RAFT-5 | Pulso | `time() - dr_raft_last_heartbeat_timestamp_seconds` |
| B-IDX | Índices | `dr_commit_index`, `dr_gate_index`, `dr_applied_index` por célula |
| B-INV-1 | Invariante de índices | `dr_applied_index <= dr_gate_index` e `dr_gate_index <= dr_commit_index` em toda célula, sempre |
| B-INV-2 | Uma líder por termo | `count by (dr_raft_term) (dr_is_leader == 1) <= 1` (precisa juntar termo e papel na mesma série, ver M-14). Duas líderes em termos **diferentes** ao mesmo tempo é normal por instantes (líder antiga isolada); no **mesmo** termo é violação. |
| B-INV-3 | Gate monotônico | `resets(dr_gate_index[janela]) == 0` fora de reinício do processo (o gate é memória e volta a 0 na partida) |
| B-APP | Applier | `dr_applier_stopped`, vazão `rate(dr_apply_item_duration_seconds_count[1m])` |
| B-DRENO | Drenagem | vazão do apply da célula − `rate(dr_writes_total{resultado="ok"}[1m])` da líder. Negativa por muito tempo = a célula nunca alcança. |
| B-K8S | Plataforma | `kube_pod_container_status_restarts_total`, `kube_pod_status_ready`, motivo do último término (`kube_pod_container_status_last_terminated_reason`), eventos de attach do volume |
| B-GO | Runtime | `go_gc_duration_seconds`, `go_goroutines`, `process_open_fds` |
| B-REDE | Rede | `container_network_transmit_bytes_total`, `container_network_receive_errors_total`, RTT entre células (blackbox exporter em gRPC ou TCP) |
| B-CUSTO | Custo | WCU consumida nas 3 tabelas / escritas confirmadas (esperado ≈ 3× por escrita) |
| B-ORC | Oráculo | séries `dr_val_*` (ORC-05) |

### 5.4 Lacunas de instrumentação (M)

Itens que os cenários pedem e o código de hoje não tem. É especificação para o Bruno decidir se entram (via SPDD); sem eles, o cenário roda, mas o item correspondente fica "não comprovado".

| Código | Lacuna | Usada em |
|---|---|---|
| M-01 | Histogramas por etapa: propose → commit, commit → gate, gate → apply local na líder | todos os de latência |
| M-02 | `dr_forward_duration_seconds{resultado}` e `dr_forwards_total{resultado}` | CF-02, CF-03, falhas da líder |
| M-03 | Histogramas nativos do Prometheus (ou baldes mais finos entre 1 ms e 100 ms) e um max por janela (gauge zerado a cada coleta, ou o maior valor visto em log estruturado) | todos |
| M-04 | Entradas e bytes do log na RAM e no disco. **Hoje no `/status`** (`raft.snapshot`, `raft.memoria.entradas`, `raft.memoria.bytes`, `raft.atrasCauda`, `wal.retido`, `wal.piso`, `wal.pinos`, `wal.tetoDuro`, `wal.janela`); as séries `dr_raft_mem_entries`, `dr_raft_mem_bytes`, `dr_raft_snapshot_index`, `dr_wal_retained_bytes`, `dr_wal_files`, `dr_retention_floor` e `dr_wal_pins` entram no Incremento B (O-42). Já existem `dr_raft_snapshot_duration_seconds` e `dr_raft_snapshot_deferred_total`. | CF-06, FP-04, FP-05, FL-* |
| M-05 | Partida: **existe** `dr_startup_duration_seconds` (abertura do WAL e releitura da cauda) e o log `partida: WAL relido` com `snapshot`, `entradas`, `primeiro` e `ultimo`. **Falta** o último índice e o termo do `HardState` como série (`dr_boot_last_index`, `dr_boot_hardstate_term`). | FP-*, FD-*, FL-05 |
| M-06 | Instante em que a célula ficou pronta (`dr_ready_since_timestamp_seconds`) | RTO-07 |
| M-07 | `dr_apply_errors_total{classe, codigo}` (classe transitório ou permanente) | FB-* |
| M-08 | Latência de leitura total por resultado (`dr_read_duration_seconds{op,resultado}`) | CF-04, FR-* |
| M-09 | `dr_write_duration_seconds` com rótulo de resultado (hoje só `ok` é medido) | todos com erro |
| M-10 | Métricas do gRPC entre células (latência e código por método: Raft, Forward, Barrier, Gate) | FR-* |
| M-11 | `dr_replication_delay_seconds{par}`: tempo entre o commit na líder e o apply na follower | FP-04, FB-01 |
| M-12 | Oráculo (ORC-05), fora do app | todos |
| M-13 | Anotação de injeção de falha no Grafana | todos de falha |
| M-14 | `dr_raft_role{termo}` ou `dr_is_leader` com o termo como rótulo, para B-INV-2 | CE-*, FR-* |
| M-15 | Sinais de laço preso enquanto ele está preso: `dr_raft_last_tick_timestamp_seconds` e `dr_wal_fsync_inflight_seconds` (idade do fsync em andamento). Hoje `dr_raft_tick_gap_seconds` e `dr_wal_fsync_duration_seconds` só são observados **depois** que o laço volta: durante um fsync travado, o painel fica em silêncio. | FD-01, FD-02, FD-04 |
| M-16 | `dr_build_info{versao}` e uma identidade do WAL (ex.: instante de criação do WAL) exposta como rótulo | FD-05, CE-07 |

### 5.5 Logs que viram evidência

`mudança de líder` (termo e líder), `gate parado`, `snapshot gravado` (índice, aplicado durável, confirmado, duração), `partida: WAL relido` (snapshot, entradas, primeiro, último, duração), `marcador enviado` e `marcador adiado` (com taxa limitada), `memória do Raft no teto`, `disco do log no teto duro`, `célula atrás da cauda`, `laço de fundo parou` (fatal), `etcdraft: wal.Save` (fatal de fsync), `erro transitório no apply, repetindo`, `escrita encaminhada à líder`. Atenção: este último é gravado em nível Info **a cada escrita encaminhada**; com milhares de TPS na follower, é volume de log que custa CPU e disco (medir em CF-02).

---

## 6. Modelo de relatório por execução

Cada execução de cenário gera: identificação (cenário, data, commit, configuração AMB-08), perfil de carga, linha do tempo com T0 e os tempos RTO, tabela de latências (p90, p99, p99.9, max por sinal), golden signals antes/durante/depois, resultado do oráculo (ORC-04), critérios de aceite com PASSOU / FALHOU / NÃO COMPROVADO, links para o painel com o intervalo fixado.

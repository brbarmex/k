# ADR-060 — O laço do Raft nunca espera o apply, e a líder recusa escrita quando o apply está atrasado

**Status:** Decidido (Bruno, 14/10/2026: respostas 1a 2a 3a 4a 5a 6a) · **Data:** 14/10/2026 · **Origem:** teste de carga da POC de 14/10 ([análise](../06_pendencias_e_riscos/analise_teste_de_carga_14_10.md)) · **Fecha:** o risco 3 da spec da POC · **Atenua:** risco 35 ([ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)) · **Precisa:** [ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)

## Contexto

Em 14/10 o Bruno rodou um teste de carga na POC: PutItem simples, 100 TPS de aquecimento, rampa até 500 TPS e 1000 TPS sustentados, tudo num notebook (três células, um LocalStack, Prometheus, Grafana e k6). O resultado foi ACK OK = 0 e eleições em série, com o termo indo de 7 para 18. Nenhuma invariante de segurança foi violada.

A cadeia que o código explica:

1. O applier já estava cerca de 210 mil entradas atrás do gate antes do teste, andando a uns 14 itens/s por célula contra o LocalStack (perto de 1 s por PutItem com 16 workers).
2. O laço do `Ready` entregava os commits por um canal bloqueante (`committed <- batch`, comentado no código como "applier lento segura o laço (risco 3)"). Com as filas do applier cheias, o `CommitPump` parava, o canal enchia e o **laço do Raft parava**. Junto com ele paravam o tick, o heartbeat, o envio de mensagens, o fsync e o `Advance`.
3. Com o laço parado a célula fica muda: a líder perde o quórum (CheckQuorum) ou as followers ficam sem heartbeat por mais que o timeout de eleição. Daí as eleições em série.
4. Sem `Advance`, o Raft não desconta o buffer de entradas não commitadas (`MaxUncommittedEntriesSize`, só descontado no `MsgStorageApplyResp`), e as propostas passam a ser descartadas (`ErrProposalDropped`, ThrottlingException). O `n.lead` só muda dentro do laço parado, então uma célula que já perdeu a liderança continua propondo e também recebe descarte.
5. O gate da POC (gate = commit) só andava quando o `CommitPump` conseguia entregar o lote ao applier. Daí as 73 mil entradas de "commit sem gate".
6. Pelo ADR-058 o ACK espera a marca d'água do apply. Com 207 mil entradas de atraso, nenhuma escrita nova tinha como ser confirmada no prazo de 7,5 s.
7. O fsync do WAL teve P95 de até 775 ms e picos de 1 a 2 s (três WALs, o LocalStack e o Prometheus no mesmo SSD), dentro do mesmo laço do heartbeat, com eleição em 1 s.

O ponto central não é da POC. Em produção, um DynamoDB lento ou com throttling numa única célula (o modo sob demanda faz throttling quando o tráfego passa do dobro do pico anterior, o que é fácil numa tabela nova ou na tabela sombra do [ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)) produz a mesma cascata: **a lentidão do banco de uma célula derruba a liderança do grupo inteiro**.

## Decisão

1. **O commit é um sinal, não uma fila (1a).**
   - O laço do `Ready` só publica o índice commitado (um atômico mais um canal fechado a cada avanço) e chama `Advance`. Ele nunca espera o `CommitPump`, o gate ou o applier.
   - O `CommitPump` lê as entradas commitadas **por faixa** da memória do Raft (`MemoryStorage`), a partir do próprio cursor, em leituras de até 1 MiB. Quem espera, quando o applier não acompanha, é o `CommitPump`, nunca o Raft.
   - O gate passa a ser alimentado direto pelo avanço do commit (goroutine própria do `CommitPump`), e não depois da fila do applier. Na POC, gate = commit volta a ser verdade o tempo todo.
   - A decodificação das entradas sai do laço: o laço só decodifica quando há uma proposta local esperando (só na líder). As followers não pagam JSON dentro do laço.
   - A fila entre o `CommitPump` e o applier passa a ser pequena (64 lotes de até 1 MiB), porque o atraso fica nas entradas que o Raft já guarda.
   - Regra de retenção: as entradas ficam na memória do Raft até o `CommitPump` consumi-las. Na POC a compactação está desligada (RT-11). Em produção a compactação do Raft é limitada a `domain.RaftCompactLimit`, cujo primeiro termo é o aplicado local, que nunca passa do que o `CommitPump` entregou; nada que o applier ainda precisa é compactado ([ADR-063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md)).
2. **Admissão por atraso do apply (2a).**
   - Antes do `Propose`, a líder recusa a escrita com ThrottlingException quando `gate - aplicado > APPLY_LAG_MAX`. Nada entra no log.
   - `APPLY_LAG_MAX` tem padrão `APPLY_WORKERS × 256`, ou 4.096 com 16 workers, o que cabe nas filas dos workers. Com o DynamoDB real (2.000 a 4.000 itens/s por shard pelo ADR-058) isso dá de 1 a 2 s de atraso.
   - A conta é contra o **gate**, não contra o commit. Entradas que esperam a Azure não são atraso do apply; o caminho delas é o modo degradado ([ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)), não a contrapressão.
   - O ACK continua esperando a marca d'água ([ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)). A admissão evita aceitar uma escrita que só venceria o prazo (PENDING) depois de entrar no log, gerar retry do SDK e ser replicada para a Azure.
3. **Timeout de eleição configurável (3a).** `ELECTION_TICKS` (de 3 a 100, padrão 10, ou 1 s com tick de 100 ms). O compose da POC usa 30 (3 s), porque o fsync do notebook tem picos de 1 a 2 s. Em produção o valor sai do spike S1, acima do P99.9 do fsync. O S1b (`AsyncStorageWrites`) ganha mais um motivo: no modo assíncrono a líder continua mandando heartbeat durante o fsync.
4. **Ambiente de teste de carga (4a).**
   - Começar de volumes limpos (`task clean`).
   - **Sem banco em memória** (Bruno, 14/10, depois da decisão 4a): ele aceita o limite do LocalStack, testa localmente com TPS baixo (por exemplo 10 TPS) e depois na AWS. Com TPS baixo o applier acompanha a carga, então o desacoplamento do laço (item 1) e a admissão (item 2) quase não são exercitados localmente. Para exercitar a admissão sem carga alta, `APPLY_LAG_MAX` pode ser baixado no compose (`task start APPLY_LAG_MAX=50`).
   - **Um LocalStack por célula** (Bruno, 14/10: 1a 2a 3a), como em produção: cada célula tem o seu escritor do DynamoDB Local, com `DYNAMODB_HEAP_SIZE=512m` por instância (o padrão é 256m). `task test-banco-lento CELL=N` pausa o banco de uma só célula e confere que o termo do Raft não muda: é a validação local do item 1 sem carga alta.
5. **Partida não reaplica o log inteiro (5a).** O ponto de retomada é o índice do progresso do applier; sem progresso, começa em 1. O Raft recebe `Applied = retomada - 1` (limitado entre o snapshot e o commit gravado) e o `CommitPump` começa na retomada; o replay vem do WAL do Raft. O progresso não tem fsync, então numa queda de host ele pode estar atrás do banco: isso só alonga o replay, que é idempotente pela versão. A entrada do progresso é reentregue inteira, e o applier pula os itens já gravados pela ordem salva.
6. **Métricas que explicam o que aconteceu (6a).**
   - `dr_throttled_total{causa}`: `buffer_cheio`, `nao_lider`, `transferencia` ou `atraso_apply`, contada na célula onde a causa nasceu (a causa do Raft é lida do `Status()` do nó na hora do descarte).
   - `dr_raft_loop_duration_seconds`: duração de cada `Ready`.
   - `dr_raft_tick_gap_seconds`: intervalo real entre dois ticks. Perto do timeout de eleição, a célula some para as outras.
   - `dr_apply_lag`, `dr_apply_lag_max`, `dr_apply_inbox_batches`, `dr_commit_undelivered` e `dr_election_ticks`.
   - Na porta de API: `dr_http_requests_total{op, codigo}` (o TPS de entrada por célula), `dr_http_request_duration_seconds{op}` (todas as respostas) e `dr_http_inflight`.
   - O `/status` ganha `apply.atraso`, `apply.atrasoMax`, `apply.fila`, `commitNaoEntregue` e `electionTicks`.
   - O dashboard v2 ganha as seções 08 (entrada HTTP e contrapressão) e 09 (apply e laço do Raft), e o painel "Resultados de escrita por segundo" deixa de empilhar as séries. Empilhado, a linha de cima era a soma de todos os resultados e parecia ser só throttling.
   - `task diag` imprime, por célula, entrada, ACK, pending, vazão e latência do apply, atraso, commit não entregue, tick e fsync P99, trocas de líder e contrapressão por causa.

## Alternativas consideradas

- **Aumentar as filas:** a fila de 4.096 lotes já era a versão "generosa" da POC e só adiou o problema. Descartado.
- **Fila ilimitada entre o laço e o `CommitPump`:** tira o bloqueio, mas duplica na memória o que o Raft já guarda e cresce até o OOM com o banco parado. Descartado.
- **Sem admissão:** o atraso do apply vira PENDING depois de 7,5 s, retry do SDK e mais entradas no log. Descartado.
- **Admissão contra o commit:** misturaria a espera pela Azure com o atraso do apply e trocaria o modo degradado por ThrottlingException. Descartado.
- **Manter a eleição em 1 s no notebook:** com picos de fsync de 1 a 2 s, qualquer teste de carga local vira teste de eleição. Descartado na POC; em produção o valor sai do S1.

## Consequências

- **O Raft fica isolado do banco da célula.** Banco lento, applier estacionado (entrada venenosa, [ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md)) ou gravação lenta do progresso do applier não param mais o heartbeat nem provocam eleição. O efeito visível passa a ser `dr_commit_undelivered` e `dr_apply_lag` crescendo e, na líder, ThrottlingException com causa `atraso_apply`.
- **Indisponibilidade de escrita explícita:** com o banco da líder lento, as escritas são recusadas na hora, sem PENDING e sem derrubar a liderança. RPO continua 0: nada recusado entrou no log.
- **Follower atrasada continua sendo um problema de RTO.** A admissão só olha a líder. Uma follower com o apply muito atrás que vira líder recusa escritas (`atraso_apply`) até alcançar o limite. O tempo para isso entra no RTO e precisa de alerta em `dr_apply_lag` das followers ([R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md)).
- **Memória:** as entradas não consumidas ficam no `MemoryStorage`. Na POC isso já acontecia, porque a compactação está desligada. Em produção a memória do Raft é limitada pela compactação até `RaftCompactLimit` e pelo teto do volume do Raft ([ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)); num modo degradado longo ela cresce com o atraso da Azure até esse teto, o que ainda precisa de mitigação (teto também por bytes em memória, ou um `raft.Storage` que lê do WAL em disco) e de medição no spike S1.
- **O timeout de eleição maior aumenta o RTO** na queda da líder: de 1 a 2 s para 3 a 6 s na POC.
- **A causa `nao_lider` continua possível** numa janela curta depois de uma troca de líder, porque `n.lead` ainda é atualizado pelo laço. Com o laço sem bloqueio a janela fica do tamanho de um `Ready`, e a métrica mostra quando acontece.
- **O teste local com TPS baixo não exercita os itens 1 e 2.** Eles ficam provados pelos testes de unidade (laço com consumidores parados, admissão) e, de verdade, no teste na AWS. O teto do apply local sai das métricas do applier (`task diag`) e, em produção, do spike S5.

## Código

- `DataReplicationApp/internal/adapter/out/etcdraft/node.go`: publicação do commit, `CommitIndex`, `CommitChanged`, `CommittedEntries`, `FirstIndex`, causa do descarte, `ElectionTicks`, `Applied` na partida, métricas do laço e do tick.
- `DataReplicationApp/internal/usecase/replicate.go`: `CommitPump` por faixa, goroutine do gate, `ResumeFrom`, `Delivered`.
- `DataReplicationApp/internal/usecase/write.go`: admissão (`MaxApplyLag`) e `BackpressureError` com a causa.
- `DataReplicationApp/internal/platform/config` (`APPLY_LAG_MAX`, `ELECTION_TICKS`), `internal/adapter/in/dynamohttp/handler.go` (métricas HTTP), `internal/adapter/out/prommetrics`, `observability/grafana/dashboards/datareplication-v2.json` e `taskfile.yaml` (`diag`, `start APPLY_LAG_MAX=...`).
- Testes: laço do Raft com consumidores parados (3.000 propostas de 64 clientes, sem eleição), causa `nao_lider`, gate da POC acompanhando o commit com o applier parado, retomada, admissão, configuração e métricas.
- Spec da POC: RT-04, RT-06, RT-07, RT-09, RT-13, AP-01, AP-12, AP-13, AP-15, O-15, CA-17 e CA-18, SG-17, risco 3 fechado, em [SPEC_POC_LOCAL.md](../../spec/SPEC_POC_LOCAL.md). Prompt do SPDD: passos O-27 (laço, admissão, retomada) e O-28 (métricas HTTP, dashboard e tasks).

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

A partida descrita no item 5 passou a sair do último snapshot-marcador, e não do log inteiro ([ADR-065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md)): só a cauda volta para a RAM, e as entradas entre o progresso e o snapshot vêm do disco pelo `LogReader`. Progresso atrás do disco retido leva a célula a `reconstruindo` ([ADR-066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md), DC-04).

## Onde está o detalhe

- [Análise do teste de carga de 14/10](../06_pendencias_e_riscos/analise_teste_de_carga_14_10.md)
- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) e [04_wal_e_fsync.md](../01_arquitetura/04_wal_e_fsync.md)
- [C35-banco-lento-nao-derruba-a-lideranca.md](../02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md)
- [D04-commitpump-e-log-do-raft.md](../04_diagramas/D04-commitpump-e-log-do-raft.md)

# SPDD Analysis: Observabilidade de sincronização, versão replicada, memória retida, recuperação e tempo de replicação

## Original Business Requirement

preciso evoluir a observabilidade do sistema, segue abaixo o que eu quero monitorar:

  - Monitorar se todas as CELLS estão sincronizadas ou seja o dado precisa estar replicado e no banco de dados
  - Monitorar a quão replicado cada cell está exemplo cell-1=drv_10 cell-2=drv_05 cell-3=drv_09 esse dado serve para acompanhar
  o quão atualizado esta as replicas
  - acompanhar o Thereshold da Memoria que cell Lider segura até a cell que está fora voltar
  - monitorar o truncamento quando ocorrer
  - monitorar o tempo de recuperacao entre cada cell
  - Monitorar o tempo de replicacao da lider

### Decisões do solicitante (24/10/2026, sobre a primeira versão desta análise)

Texto original das respostas:

> decisoes:
> 1. qual sua recomendacao?
> 2. Memomeria que me refiro é aquela que o raft segura quando uma follwer esta fora e vai crescendo ate um teto.
> 3. desconsiderar... abandone essa.
> 4. é o tempo que o dado replicado do banco é commitado em todas as cell, ou seja quando a lider confirmar que todas as cells comitaram
> 6. somente a poc local, e precisa atualizar o dashboard do grafana

Como cada resposta entra nesta análise:

- **D-01 (sincronizada):** o solicitante pediu uma recomendação. **A recomendação adotada é por tempo, no relógio da líder.** Uma célula está sincronizada quando a entrada mais antiga que a líder ainda não viu aplicada no banco dela tem idade ≤ **2 s**. O cluster está sincronizado quando as três células estão. Ficar fora do limite por **mais de 15 s** é dessincronia (vermelho no painel). Com a carga parada, a sincronia exige distância **zero** (convergência). Os números ficam como limites do painel, ajustáveis sem tocar no app. O motivo: a tolerância em entradas depende da taxa de escrita (10 entradas são 1 ms a 10 mil TPS e 10 s a 1 TPS), e a tolerância em tempo não.
- **D-02 (memória):** é a **RAM do Raft** (o `MemoryStorage`), que a líder segura enquanto uma follower está fora e que cresce até o teto `RAFT_RAM_MAX`. O disco do log (`RETAIN_MAX_BYTES`, teto duro) e a memória do processo (RSS, heap) **não fazem parte** deste item.
- **D-03 (truncamento):** **fora do escopo.** O item "monitorar o truncamento quando ocorrer" é abandonado nesta feature.
- **D-04 (dado "commitado em todas as cells"):** o tempo que importa é até a **líder confirmar que todas as células gravaram o dado no banco**, ou seja, até a líder ver o aplicado de cada célula ≥ o índice da entrada. A resposta veio no item 4 (tempo de recuperação), mas o texto descreve a mesma régua que o item 5 da primeira versão (tempo de replicação da líder), que ficou sem resposta. **Leitura adotada, a confirmar:** é uma régua só, usada em duas medidas.
  - **Tempo de replicação da líder:** do momento em que a líder propõe a entrada até confirmar que ela está no banco das três células. O tempo até o commit pelo quórum e até o banco da líder ficam como etapas auxiliares.
  - **Tempo de recuperação de uma célula:** do momento em que a célula fica fora (a líder deixa de ver o aplicado dela avançar) até a líder confirmar que ela aplicou tudo até o commit.
- **D-06 (ambiente):** **só a POC local** (compose, `observability/prometheus`, `observability/grafana`). A `infra-aws/` está fora. **Atualizar o dashboard do Grafana é obrigatório.** Regras de alerta e Alertmanager não foram pedidos e ficam fora; os limites aparecem como cores e thresholds dos painéis.

---

## Domain Concept Identification

### Existing Concepts (from codebase)

- **Célula (`celula`)**: unidade de réplica (cell-1, cell-2, cell-3), cada uma com o próprio nó Raft, WAL e tabela DynamoDB. Toda série Prometheus já leva o rótulo constante `celula` (`prommetrics.NewRecorder`).
- **Índices da célula: commit → gate → aplicado**: `dr_commit_index`, `dr_gate_index` e `dr_applied_index` são exportados por célula pelo `StateCollector` a partir do `CellStatus` (`usecase/status.go`). O **aplicado** é a marca d'água do applier (ADR-048, ADR-058): tudo até ele está no banco da célula. A invariante `aplicado ≤ gate ≤ commit` já tem painel (linha 02 do dashboard).
- **Versão do item (`_dr_v`)**: atributo gravado em cada item do DynamoDB com o índice Raft da escrita (`VersionedOp.Version`, condição de versão em `dynamodb/store.go`). O "drv_10" do requisito é esse índice. A versão garantida da célula inteira é a marca d'água do aplicado.
- **Match e lag por par (só na líder)**: `dr_peer_match_index{par}` e `dr_peer_lag{par}`. O match diz até onde o par **persistiu o log**, não até onde ele **gravou no banco**.
- **Publicação do gate líder → follower (`GateUpdate`)**: RPC de `api/peerv1`, publicada pelo `GateSync.Tick` da líder a cada avanço do gate e, sem avanço, a cada `GATE_PUBLISH_INTERVAL` (100 ms), e enviada por `peerclient.pumpGate` com coalescência. A resposta hoje é `Empty`, e a follower não devolve nada à líder. **É o canal natural para a líder passar a conhecer o aplicado de cada follower** (D-04).
- **Atividade dos pares**: `RecentActive` do Progress do etcd/raft (`etcdraft/node.go`) alimenta `dr_quorum_active`. É o sinal de "par vivo" que a líder já tem.
- **Log do Raft na RAM (`MemoryStorage`)**: o `ramTracker` (`etcdraft/ram.go`) conta entradas e bytes por índice (`RaftInfo.MemEntries`, `MemBytes`), e o teto é `RAFT_RAM_MAX` (256 MiB na POC). Na líder, o `Compactor.compactRAM` só compacta até o menor Match das réplicas na janela (DC-08): é a follower fora que faz a RAM crescer. No teto, a compactação vai até o snapshot e ignora o Match (SG-23), e a follower deixa de poder voltar só pelo `MsgApp`. Os valores estão no `/status` (`raft.memoria`), mas **não são séries Prometheus** (lacuna M-04).
- **Snapshot-marcador e atraso da cauda**: `dr_raft_snapshot_deferred_total` e `raft.atrasCauda` (`BehindTail`) mostram a follower que ficou atrás da RAM da líder. No Incremento A, ela fica parada nesse estado até o Incremento B.
- **Estado da célula diante do log (`CellMode`)**: os estados normal, recuperando e reconstruindo estão modelados em `domain/cellstate.go`, mas **não estão ligados** no app. A recuperação (`CatchUp`) e a reconstrução são do Incremento B (O-38 a O-44), que ainda não foi entregue.
- **Porta de métricas (`port.Metrics`) + `StateCollector`**: o padrão estabelecido. **Eventos** (contadores e histogramas) entram pela porta, que nunca bloqueia. **Estado** é lido no scrape, a partir do mesmo retrato do `/status`. Só o `prommetrics` importa o `client_golang`.
- **Dashboard e Prometheus da POC**: `observability/grafana/dashboards/datareplication.json` (9 linhas, provisionado por `observability/grafana/provisioning`) e um Prometheus com coleta de 1 s em `cell-1..3:9090`. Não há regras de gravação nem de alerta.
- **Catálogo de validação**: `validation/00_metodo_e_observabilidade.md` lista como lacunas M-01 (latência por etapa), M-04 (RAM e disco do log como séries) e M-11 (`dr_replication_delay_seconds{par}`). Esta feature fecha M-11, a parte RAM de M-04 e parte de M-01.

### New Concepts Required

- **Aplicado da follower conhecido pela líder**: a líder passa a saber, por par, até onde cada follower gravou no banco. Sem isso, nenhuma das medidas da D-04 é possível. A follower informa o próprio aplicado, e a líder guarda o último valor e o instante em que o recebeu. É uma **mudança de contrato entre células**, só de leitura (não afeta gate, commit nem ACK).
- **Estado de sincronização (por célula e do cluster)**: sincronizado ou não pela régua da D-01. É derivado do atraso em tempo de cada célula, calculado pela líder.
- **Versão replicada por célula**: a marca d'água do `_dr_v` de cada célula ("cell-N = drv_X") e a distância até o commit da líder, nas duas visões: a de cada célula (o próprio aplicado) e a da líder (o aplicado informado por par).
- **Relógio de replicação na líder**: registro limitado do instante em que cada índice foi proposto e commitado na líder. Dele saem as idades e durações sem comparar relógios de máquinas diferentes.
- **Tempo de replicação completa**: duração de propose até "aplicado em todas as células" (D-04), com as etapas auxiliares propose → commit (quórum) e commit → aplicado na líder.
- **Atraso de replicação por par (em segundos)**: idade da entrada mais antiga commitada que a líder ainda não viu aplicada no par. É a base do estado de sincronização (D-01) e o M-11 do catálogo.
- **Memória retida para a follower fora**: bytes e entradas da RAM do Raft na líder, contra `RAFT_RAM_MAX`, e quanto disso é atribuível a cada par (bytes desde o Match do par). Inclui o evento "teto atingido", em que a líder abandona o par.
- **Sessão de recuperação de uma célula**: intervalo entre a célula **ficar fora** e a **líder confirmar que ela aplicou tudo até o commit**. Mede a duração por célula e indica se a sessão terminou pelo caminho normal (`MsgApp`) ou se está presa atrás da cauda.

### Key Business Rules

- **Sincronizada significa "no banco", confirmado pela líder**: só o aplicado conta; o Match não serve (D-01, D-04). Governa: Estado de sincronização, Versão replicada.
- **Régua de sincronia por tempo**: a célula está sincronizada quando seu atraso de replicação é ≤ 2 s. Mais de 15 s fora é dessincronia. Com a carga parada, a distância precisa chegar a zero. Os limites ficam no painel (D-01). Governa: Estado de sincronização.
- **Sem líder, não há referência**: durante a eleição, o estado é "desconhecido", não "sincronizado". Uma célula que a líder não ouve ou que não responde ao scrape conta como **não sincronizada**. Governa: Estado de sincronização, Sessão de recuperação.
- **A marca d'água é contígua; o `_dr_v` por item não é**: com o apply paralelo por chave, itens acima do aplicado podem já estar no banco. A versão reportada é sempre a marca d'água. Governa: Versão replicada.
- **Um relógio só**: toda duração entre células é medida no relógio da líder (D-04). Governa: Tempo de replicação, Atraso por par, Sessão de recuperação.
- **A memória retida tem dono e teto**: a RAM só passa da janela normal (`RAFT_CATCHUP_ENTRIES`) por causa de um par atrasado, e nunca passa de `RAFT_RAM_MAX`. No teto, a líder abandona o par (SG-23), que só volta pelo Incremento B. Esse é o fim do "até a cell que está fora voltar" quando a célula não volta a tempo (D-02). Governa: Memória retida, Sessão de recuperação.
- **Observabilidade não muda o protocolo de dados**: o aplicado informado pela follower serve só para medir. Gate, commit, ACK, piso de retenção e compactação continuam decididos como hoje. Governa: Aplicado da follower conhecido pela líder.
- **Salvaguarda de métricas**: nada novo bloqueia o laço do Ready nem aloca por entrada nele. Governa: todos.
- **Índices em memória voltam a zero no reinício**: o aplicado reportado pode "recuar" quando a follower reinicia. A líder aceita o recuo como reinício e começa uma sessão de recuperação, sem tratar como erro. Governa: Versão replicada, Sessão de recuperação.

---

## Strategic Approach

### Solution Direction

- **Evoluir a observabilidade existente, sem um subsistema novo**: o estado é lido no scrape (`StateCollector`) e os eventos passam pela porta `port.Metrics`. Só o `prommetrics` conhece o Prometheus. O dashboard ganha uma linha nova, **"10 · Sincronização, memória retida e recuperação"**, e as linhas existentes que tratam de lag (03, Raft) passam a apontar para ela.
- **Fluxo**:
  1. A follower informa à líder o próprio aplicado, aproveitando o canal líder ↔ follower que já existe (o `GateUpdate`).
  2. A líder cruza o aplicado de cada par com o próprio relógio de replicação (instante de propose e de commit por índice) e com a RAM do Raft.
  3. A líder exporta por par: aplicado conhecido, distância em entradas, atraso em segundos, bytes de RAM atribuídos e duração da sessão de recuperação. Exporta também o histograma do tempo de replicação completa e das etapas.
  4. Cada célula continua exportando o próprio aplicado, commit e gate.
  5. O Prometheus junta as séries por `celula` e `par`, e o Grafana mostra: a versão por célula ("cell-N = drv_X"), o estado de sincronia do cluster com a régua D-01, a RAM retida contra o teto, a linha do tempo de recuperação e a latência de replicação.
- **Exportar o que já está calculado**: RAM do Raft (entradas, bytes, teto), snapshot e atraso da cauda passam do `/status` para séries. Isso é a parte RAM de M-04, com custo baixo e ganho imediato.
- **Recuperação já pelo caminho `MsgApp`**: a sessão termina quando a líder vê o aplicado do par alcançar o commit. Se a RAM bate no teto e o par fica atrás da cauda, a sessão fica aberta e marcada como presa até o Incremento B. As transições do `CellMode` (recuperando, reconstruindo) ficam declaradas como **ponto de extensão** do Incremento B, sem implementação aqui.
- **Atualizar a documentação de validação**: M-11, a parte RAM de M-04 e a parte "propose → commit → aplicado" de M-01 deixam de ser lacunas no catálogo de `validation/00_metodo_e_observabilidade.md`.

### Key Design Decisions

- **Como a líder fica sabendo do aplicado das followers** (consequência da D-04): (a) a resposta do `GateUpdate` passa a levar o aplicado da follower. Reaproveita a chamada que já existe e mantém o caminho líder → follower, e o `GateUpdate` já é reenviado a cada 100 ms mesmo sem escrita, então a resposta chega também com a carga parada. (b) uma RPC nova, follower → líder e periódica. Independe do gate, mas acrescenta um canal e exige que a follower saiba quem é a líder. (c) calcular tudo no Prometheus, com o aplicado de cada célula. Não muda o contrato, mas não atende "a líder confirmar" e usa o relógio do scrape (1 s). → **Recomendo (a)**. A cadência de 100 ms já existe, então a líder recebe o aplicado mesmo com a carga parada e a convergência a zero fica visível, sem RPC nova. O codec próprio do `peerv1` precisa aceitar o campo novo sem quebrar uma célula de versão anterior (a resposta antiga continua vazia, e a líder trata a falta do campo como "desconhecido").
- **Relógio de replicação: um carimbo por índice ou por amostragem**: um carimbo por índice é exato, mas cresce com a follower fora, que é justamente o caso medido, e aloca no laço. Por amostragem (um carimbo a cada intervalo fixo, por exemplo 10 ms, ou a cada N índices, num buffer circular limitado), a precisão é limitada ao intervalo. → **Amostragem num buffer circular de tamanho fixo**, gravado sem trava no caminho do commit e lido fora do laço. A precisão de dezenas de ms basta para uma régua de 2 s. Quando a idade passa do horizonte do buffer, o atraso aparece como "≥ horizonte", o que já é dessincronia.
- **Tempo de replicação: o que é o número principal**: (a) propose → aplicado em todas as células (D-04); (b) propose → commit pelo quórum; (c) commit → aplicado na líder. → **(a) é o principal; (b) e (c) são etapas auxiliares no mesmo painel**. Com uma célula fora, (a) não se completa: a líder mede (a) só sobre as células sincronizadas ou mostra (a) "aberto". **Recomendo** exportar (a) como "todas as células" e também como "quórum de bancos" (2 de 3), para que uma célula fora não esconda a latência real do sistema. Isso precisa de confirmação (ver ambiguidades).
- **Versão por célula: de quem é a fonte**: o aplicado que cada célula exporta é exato, mas some quando a célula cai. O aplicado que a líder conhece está atrasado por um envio, mas está presente mesmo com a célula fora, junto com o instante da última notícia. → **Os dois no painel**: "cell-N = drv_X" pela própria célula e a visão da líder ao lado, com a idade da última notícia. Para o estado de sincronia, a visão da líder é que decide.
- **Memória retida: total ou por par**: o total contra o teto responde "quanto falta para abandonar a follower". Por par, responde "quem está segurando". → **Os dois**. O total e o teto vêm do `ramTracker` (lidos no scrape). A parte por par é calculada uma vez por ciclo do `Compactor` (1 s), porque somar bytes desde o Match de cada par a cada scrape custa O(entradas na RAM) sob trava. O evento "teto atingido" vira contador, para não se perder entre dois scrapes.
- **Sessão de recuperação: onde mora o estado**: (a) na líder: início quando ela deixa de ver o aplicado do par avançar com atraso acima da régua (ou o par sem `RecentActive`), e fim quando o aplicado do par alcança o commit. (b) em PromQL, pelo tempo com `atraso > limite`. (c) na própria célula que volta. → **(a)**, porque é a régua da D-04 ("quando a líder confirmar"). Exporta a duração da sessão em curso (gauge por par) e a duração das sessões encerradas (histograma por par). Na troca de líder, a nova líder começa a sessão quando passa a liderar. Isso fica documentado como limite da medida.
- **Alertas**: **fora** (D-06). Os limites da D-01 (2 s e 15 s) e da RAM (por exemplo, 70% e 90% de `RAFT_RAM_MAX`) aparecem como thresholds e cores nos painéis.

### Alternatives Considered

- **Calcular a sincronia só no Prometheus, sem mudar o contrato entre células**: não atende "quando a líder confirmar" (D-04), perde a célula fora do scrape e depende do relógio do scrape. Fica como visão complementar no painel, não como fonte do estado.
- **Scan das tabelas para achar o maior `_dr_v` de cada célula**: custa RCU, é lento e não prova contiguidade. Rejeitado; a marca d'água é a informação certa.
- **Carimbo de tempo dentro da entrada ou do item, comparado na follower**: depende de relógios de hosts diferentes. Rejeitado em favor do relógio único da líder.
- **Tracing distribuído (OpenTelemetry)**: dependência nova, coletor e custo no laço do Ready. Rejeitado para a POC.
- **Monitorar também o disco do log e o truncamento**: fora do escopo pelas D-02 e D-03.

---

## Risk & Gap Analysis

### Requirement Ambiguities

- **Interpretação da resposta 4 (D-04)**: a resposta foi dada para o tempo de recuperação, mas descreve a régua do tempo de replicação. A análise adota uma régua só para as duas medidas. **Confirmar.**
- **Tempo de replicação com uma célula fora**: "confirmar que todas as cells comitaram" nunca se completa enquanto uma célula está fora. O número principal deve ser sobre as três células (fica aberto, ou só conta as sincronizadas) ou também sobre o quórum de bancos (2 de 3)? A análise recomenda exportar os dois.
- **Limites da régua D-01**: 2 s e 15 s são a recomendação, não um pedido. Confirmar, ou substituir pelos valores do negócio (por exemplo, o RPO e o RTO desejados).
- **Limites da RAM retida**: os níveis de atenção e crítico (70% e 90% de `RAFT_RAM_MAX` como sugestão) não foram definidos.
- **Início da sessão de recuperação**: começa quando a líder deixa de ouvir o par, quando o atraso passa de 2 s ou quando o processo da célula cai? A análise recomenda "atraso acima da régua ou par sem `RecentActive`", o que for primeiro.
- **"Entre cada cell"**: a leitura adotada é uma duração por célula. Se a intenção for por par de origem e destino (como no `CatchUp` do Incremento B), isso fica para o Incremento B.

### Edge Cases

- **Troca de líder**: a nova líder não tem histórico de carimbos nem de aplicados. Sessões em curso recomeçam, e as séries "só na líder" mudam de célula. O painel precisa juntar por `par` e filtrar pela líder atual, para não dobrar séries.
- **Follower de versão anterior (rolagem, CE-07)**: responde ao `GateUpdate` sem o aplicado. A líder mostra "desconhecido" e não "sincronizado".
- **Carga parada**: o gate não avança, mas a líder republica a cada `GATE_PUBLISH_INTERVAL`, e a resposta continua trazendo o aplicado. A convergência a zero aparece.
- **Reinício da follower**: o aplicado reportado recua e sobe de novo pelo replay. É sessão de recuperação, não violação.
- **Applier parado em entrada venenosa**: o log está em dia, mas o aplicado não anda, e o atraso cresce indefinidamente. O painel mostra `dr_applier_stopped` ao lado, para separar essa causa de falha de rede.
- **RAM no teto**: a líder abandona o par, e a RAM retida "cai" justamente quando a situação piora. O contador de teto atingido e a sessão marcada como presa atrás da cauda evitam ler isso como melhora.
- **Entradas sem item** (vazias, ConfChange): avançam o commit sem gravar no banco. A marca d'água já as trata como aplicadas, e a régua não pode esperar por uma gravação que não existe.
- **Atraso maior que o horizonte do buffer de carimbos**: a idade passa a ser "≥ horizonte". O painel precisa mostrar o valor saturado como dessincronia, não como número exato.

### Technical Risks

- **Mudança de contrato entre células**: o codec do `peerv1` é próprio (`api/peerv1/codec.go`), e o campo novo na resposta precisa ser compatível nos dois sentidos durante a rolagem. É o maior risco da feature e deve ter teste de compatibilidade.
- **Custo no laço do Ready**: o carimbo de propose e commit fica no caminho quente. Precisa ser escrita sem trava num buffer fixo, sem alocação, e verificado em `dr_raft_loop_duration_seconds` antes e depois (CF-05).
- **Resposta do `GateUpdate` maior**: a cadência de 100 ms já existe; o custo novo é só o campo do aplicado na resposta, e o `pumpGate` continua coalescido.
- **Atribuição de RAM por par**: o cálculo é O(entradas na RAM) sob a trava do `ramTracker`. Feito no ciclo do `Compactor` (1 s) e publicado pronto, o custo fica fora do scrape e do Ready.
- **Coordenação com o Incremento B (O-42)**: a O-42 reservou os gauges do `/status` e o dashboard. Esta feature assume a parte RAM e os nomes das séries. O prompt do Incremento B precisa ser ajustado via `/spdd-prompt-update` para não duplicar.
- **Dashboard em JSON editado à mão**: uma linha nova pode quebrar o provisionamento. É preciso validar a carga no Grafana local depois da mudança.
- **Resolução do scrape (1 s)**: durações curtas só aparecem como histogramas e contadores, nunca como gauges amostrados.

### Acceptance Criteria Coverage

Os ACs são os itens do requisito, ajustados pelas decisões D-01 a D-06.

| AC# | Description | Addressable? | Gaps/Notes |
|-----|-------------|--------------|------------|
| 1 | Monitorar se todas as CELLS estão sincronizadas (dado replicado e no banco) | Yes | A régua por tempo (D-01) é calculada pela líder a partir do aplicado informado por cada follower (D-04). Exige a mudança de contrato no `GateUpdate`. A célula fora conta como não sincronizada. Os limites de 2 s e 15 s são a recomendação, a confirmar. |
| 2 | Monitorar a quão replicado cada cell está (cell-N = drv_X) | Yes | A marca d'água do aplicado por célula (já existe) e a visão da líder por par, com a distância ao commit. Novo painel no Grafana. |
| 3 | Acompanhar o threshold da memória que a líder segura até a cell fora voltar | Yes | A RAM do Raft (D-02): o total contra `RAFT_RAM_MAX`, a parte de cada par e o contador de teto atingido. Os níveis de alerta visual ainda não foram definidos. |
| 4 | Monitorar o truncamento quando ocorrer | N/A | Fora do escopo por decisão do solicitante (D-03). |
| 5 | Monitorar o tempo de recuperação entre cada cell | Partial | A sessão por célula na líder, do "ficou fora" até "a líder confirma o aplicado = commit" (D-04). Só pelo caminho `MsgApp`: uma célula abandonada no teto da RAM fica com a sessão aberta e marcada como presa até o Incremento B. Falta confirmar o início da sessão e a leitura "por célula". |
| 6 | Monitorar o tempo de replicação da líder | Yes | Propose → aplicado em todas as células (D-04), com as etapas propose → commit e commit → aplicado na líder, no relógio da líder. Falta definir como contar quando uma célula está fora (todas as células ou quórum de bancos). |
| 7 | Atualizar o dashboard do Grafana (POC local) | Yes | Nova linha "10 · Sincronização, memória retida e recuperação" e ajuste da linha 03. Somente a POC local (D-06), sem alertas. |

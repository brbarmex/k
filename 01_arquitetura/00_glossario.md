# Glossário e códigos de referência

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md)

## Estrutura e componentes

| Termo | Significado |
|---|---|
| **DataReplication** | O app único de cada célula. Recebe as chamadas do AWS SDK (API compatível com DynamoDB), encaminha escritas à líder, participa do Raft, mantém os dois logs e aplica no banco da célula. Não há sidecar |
| **Célula** | Um cluster Kubernetes (EKS ou AKS) em **uma** AZ, com as apps do cliente e um `DataReplication` no mesmo namespace. Há 3 células na AWS (A1 a A3) e 3 na Azure (Z1 a Z3) |
| **Shard** | Unidade de ordenação e de consenso: um log, uma líder. No v1 a ordenação é por shard |
| **AZ / região** | AZ: datacenter independente dentro de uma região. Aqui: AWS `sa-east-1` e Azure `Brazil South`, ambas em São Paulo |
| **Compass-Router** | Roteador externo que leva cada cliente a uma célula. Fora do escopo do time |
| **Agent** | Processo do control plane que pede ao Compass-Router para mudar o destino do tráfego. Fora do escopo |
| **Cloud-Adapter** | Componente que traduz o formato do DynamoDB para o Cosmos DB (inclui expressão condicional e TTL). Outro componente, não faz parte do `DataReplication` |
| **Applier** | Rotina que grava as entradas commitadas no banco da célula, com escrita condicional por versão, sempre depois do gate. Um por tabela por célula. Persiste o progresso **por item** `(índice, ordem)` no volume do log (ADR-048) |
| **CommitPump** | Rotina que recebe as entradas commitadas e as distribui ao gate, ao applier e ao log de replicação |
| **Link dedicado** | Conexão privada (cabo físico) entre AWS e Azure, sem internet |
| **`drctl`** | CLI operacional: reconstruir réplica (`drctl rebuild`), transferir liderança, inspecionar, promover, extrair escritas órfãs |

## Consenso e replicação

| Termo | Significado |
|---|---|
| **Raft** | Protocolo que faz os nós concordarem sobre a ordem das entradas e sobre quem é a líder. Biblioteca: `go.etcd.io/raft/v3` |
| **Votante** | Nó que participa da eleição e do commit. Só as 3 células AWS |
| **Learner** | Nó que recebe o log e informa o índice durável (pelas respostas do `AppendEntries`, com termo e identidade), mas **não vota**. As 3 células Azure |
| **Líder / follower** | Líder: a célula que propõe entradas. Follower: encaminha escritas à líder (1 salto) |
| **Termo / epoch** | Geração da liderança. Termo: do Raft; o gate só adota progresso do termo atual. Epoch: da configuração do grupo (promoção) |
| **Gate** | Índice até onde ACK e applier podem avançar: `min(commit do Raft, 2º maior índice durável dos learners)`. Nunca recua. **Só conta progresso confirmado pelo Raft no termo atual e é alimentado por evento** (ADR-046). No modo degradado, ignora a Azure |
| **Log do Raft** | Log do consenso, de vida curta, com fsync antes do ACK |
| **Log de replicação** | Log nosso, de vida longa, segmentado (nosso), em volume próprio, gravado depois do commit sem fsync; serve a Azure atrasada, o `drctl` e a reconstrução ([ADR-055](../03_adr/ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)) |
| **Checkpoint durável / `DurableIndex`** | `fsync` do segmento ativo do log de replicação a cada `REPLOG_SYNC_INTERVAL`; o maior índice coberto por ela é o `DurableIndex`, limite até onde o log do Raft pode ser compactado |
| **`RAFT_RETAIN_MAX`** | Folga que o log do Raft guarda para seguidores e learners pouco atrasados; acima dela, o catch-up é pelo log de replicação |
| **Segmento** | Arquivo pré-alocado do `wal` do etcd (log do Raft). O motor próprio `segfile` saiu em 13/10 (ADR-055) |
| **Eleição / heartbeat / PreVote / CheckQuorum** | Escolha de nova líder; sinal de vida da líder; proteções do Raft contra eleições à toa e líder isolada |
| **Fencing** | Impedir que um escritor antigo continue gravando depois de perder o cargo, por termo, epoch ou versão. Na partição entre as nuvens, o fencing vem do **lease** (ADR-044) |
| **Split-brain** | Duas partes aceitando escritas ao mesmo tempo, cada uma achando que é a líder |
| **TLA+ / Jepsen** | TLA+: especificação formal do protocolo. Jepsen: teste que injeta falhas de rede, disco e relógio |

## Escrita e leitura

| Termo | Significado |
|---|---|
| **Entrada** | Unidade replicada do log. Um `BatchWriteItem` vira uma entrada (ADR-035, C23) |
| **Índice** | Posição da entrada no log; também é a **versão** do item no banco |
| **Post-image** | O item como fica depois da escrita; é o que vai para o log |
| **`_dr_v`** | Atributo reservado (nome a definir) com a versão do item. O applier grava só se a versão guardada for menor. **Nunca aparece nas respostas** (ADR-051) |
| **Commit** | Entrada gravada com fsync em 2 de 3 votantes AWS |
| **ACK** | Resposta de sucesso ao cliente: depois do gate e do apply no banco da célula da líder (e, no modo degradado, com o lease válido) |
| **Apply** | Gravação da entrada no banco (DynamoDB ou Cosmos). Sempre serial e depois do gate (ADR-057) |
| **`appliedIndex` / `last_applied`** | Último índice aplicado por inteiro no banco local. Nunca passa do gate |
| **Strong consistency** | Único modo do `DataReplication` (ADR-057): escrita serial depois do gate e leitura sempre forte na tabela base, pela barreira `min(ReadIndex, gate)`. As aplicações enviam `ConsistentRead=true`. Query em GSI é recusada (ADR-050) |
| **ReadIndex** | Leitura forte: a líder confirma que ainda é líder e informa o índice que a leitura precisa alcançar |
| **Barreira da leitura forte** | `min(ReadIndex, gate)`: o que a leitura forte espera que o `appliedIndex` alcance. Desacopla a leitura da saúde da Azure; na follower, `ReadIndex` e gate chegam na mesma resposta (ADR-049) |
| **Dedupe** | Reconhecer o retry de uma escrita pelo id estável do SDK, numa janela de 5 min, **por requisição inteira** (não por item), para não aplicá-la duas vezes. Fora da janela vale a semântica do DynamoDB (ADR-051) |
| **`PENDING`** | `503` quando o prazo estoura depois do commit: a escrita não se perdeu e o retry é absorvido pelo dedupe. Com `DEGRADED_POLICY=reject`, também é a resposta quando falta o gate da Azure. A consulta devolve `APPLIED`, `REJECTED` ou `PENDING` **por item** |
| **`503 LEASE_EXPIRED`** | A AWS recusa escrita porque o lease do modo degradado acabou. Leitura e Raft seguem |
| **`REQUEST_DEADLINE` / `DEGRADE_AFTER`** | Prazos: 7,5 s da requisição e 2 s de espera pela Azure antes do modo degradado |
| **TTL / GSI / Scan** | Usos dos clientes: expiração por item (local por célula), índice global secundário (Query e Scan em GSI são recusados, ADR-050), varredura (sem snapshot) |

## Falhas, recuperação e operação

| Termo | Significado |
|---|---|
| **RPO / RTO** | Dado confirmado que pode se perder / tempo para voltar. RPO = 0 em operação normal (e sempre, com `DEGRADED_POLICY=reject`); RTO = **distribuição medida em exercícios de falha** (mediana, P95, pior caso), não soma de P99, com piso D + M se a AWS morre em modo degradado |
| **SLO ponta a ponta** | Latência, erro e recuperação medidos sobre a operação completa, da requisição à resposta. Publicado como `overhead + escrita no banco` (ADR-036) |
| **Modo normal / degradado** | Normal: o gate exige a maioria Azure (RPO 0). Degradado (só com `DEGRADED_POLICY=accept`): a Azure está inalcançável e a AWS confirma sozinha (RPO > 0) **enquanto o lease valer**, sinalizado por `x-dr-mode: degraded` e métrica |
| **`DEGRADED_POLICY`** | Variável **obrigatória, sem padrão**, por instalação: `accept` (modo degradado com lease) ou `reject` (nunca degrada; sem gate o cliente recebe `503 PENDING`; RPO 0 sempre). ADR-045 |
| **Lease / `DEGRADED_LEASE` / D** | Validade do modo degradado, carregada pela entrada `DEGRADE`. D é o teto de duração do RPO > 0, **valor a definir** (PEND-02), contado no relógio monotônico da líder **desde o último contato com a maioria dos learners da Azure** (`t_c`), não desde a proposta do `DEGRADE`, e conferido a cada ACK. Líder nova ou nó reiniciado em degradado trata o lease como expirado até contato com a Azure ou `RENEW`. Ao fim de D a AWS para sozinha. ADR-044 |
| **M (margem)** | Deriva máxima de relógio + pausa máxima de processo (`DEGRADE_AFTER` não entra: o lease conta desde `t_c`). A Azure só é promovida depois de **D + M** desde o último contato da AWS; `ForcePromote` recusa antes disso. Valor a definir (PEND-17) |
| **`DEGRADE` / `RESTORE` / `RENEW`** | Entradas replicadas que mudam a regra do gate para degradado (com validade D), de volta, e prorrogam o lease (`RENEW`, só por ação humana) |
| **Episódio de degradação** | Período contínuo em modo degradado; seu `fromIndex` define a janela de RPO > 0 |
| **Ressincronização / catch-up** | A Azure recebendo o que perdeu até alcançar a AWS |
| **Promoção / `ForcePromote`** | Tornar a Azure líder quando a AWS cai. Sempre decidida por uma pessoa, e só depois de D + M |
| **Failover / failback** | Passar a operação para a outra nuvem / voltar |
| **Escritas órfãs** | Escritas ACKadas na AWS durante o modo degradado que não chegaram à Azure antes da promoção |
| **Entrada venenosa / `SKIP`** | Entrada commitada com um item que o banco recusa sempre e que trava o applier. O `SKIP` identifica **o item** `(índice, ordem)`, é sempre humano e auditado (ADR-048) |
| **`I-DEL`** | Invariante do DELETE real: com as leituras abertas, o estado materializado da célula é a dobra do log até `applied` (ADR-047) |
| **Tabela sombra / `F` / cutover** | Reconstrução de banco: registra o índice `F` do log, copia a tabela por varredura, reaplica o log desde `F`, pausa a escrita, troca o nome lógico para a tabela sombra (cutover) e verifica por checksum |
| **Geração do nome lógico** | Versão do mapeamento nome lógico para tabela física; o cutover troca a física sem mudar o nome que o cliente vê |
| **Anti-entropia / checksum por faixa** | Verificação periódica ou pós-rebuild de que as células guardam o mesmo conteúdo; detecta divergência, não a previne. Métrica `last_reconciled` |
| **Automático / assistido / manual** | Quem age em cada falha: o sistema; o sistema com aprovação de uma pessoa; uma pessoa. Matriz em [08](08_resiliencia_autocura_e_timeouts.md) (9.5) |
| **`last_committed`, `last_azure_durable`, `last_acked`, `last_applied`, `last_reconciled`** | Métricas de garantia por shard, uma por tipo de índice (ADR-046). Mais `lease_remaining` e `poison_entry_index` |
| **Falha cinzenta** | Lentidão ou perda parcial em que nada está formalmente fora, mas tudo piora |
| **Flapping / histerese** | Cair e voltar repetidamente / exigir estabilidade por um tempo antes de mudar de estado |
| **Throttling / 429 / RU / WCU** | Banco recusando por carga / código HTTP / unidades de capacidade do Cosmos / do DynamoDB |
| **mTLS / IAM / IRSA** | TLS com autenticação dos dois lados / permissões da nuvem / papel IAM por carga de trabalho no EKS |
| **mTLS por aplicação** | Cada aplicação do cliente apresenta um certificado ao `DataReplication`, que consulta a lista identidade → tabelas e operações; sem entrada, `403`. O SDK precisa do certificado cliente: "só trocar o endpoint" deixou de ser verdade (ADR-052) |
| **Suíte de conformidade / inventário** | Mesma sequência de chamadas contra o DynamoDB real, a camada e o Cosmos pelo Cloud-Adapter, e levantamento das chamadas reais de cada cliente, antes de ele entrar (ADR-051) |
| **PITR** | Restaurar o banco como estava num instante passado |
| **Game day** | Simulação planejada de desastre para treinar e medir |

## Códigos usados nos documentos

| Código | O que é | Onde |
|---|---|---|
| **C01 a C34** | Cenário **atualizado** (modelo v1) | [`02_cenarios/atualizados/`](../02_cenarios/atualizados/) |
| **X01 a X06** | Cenário **complexo**, detalhado em arquivo próprio | [`02_cenarios/complexos/`](../02_cenarios/complexos/) |
| **R03 a R23** | Cenário ou hipótese **a rever** (os 14 que ainda não têm decisão) | [`02_cenarios/a_rever/`](../02_cenarios/a_rever/) |
| **ADR-001 a 055** | Registro de decisão de arquitetura (044 a 054 vêm da revisão crítica de 13/10; 055, do log de replicação segmentado) | [`03_adr/`](../03_adr/INDEX.md) |
| **D1 a D13** | Decisões da revisão de 12 e 13/10 (D9 gate, D10 header, D11 dedupe, D12 TLA+, D13 prazos) | [`ADR`](../03_adr/INDEX.md) correspondentes |
| **PEND-nn** | Pendência com dono e padrão até a decisão | [`06_pendencias_e_riscos/`](../06_pendencias_e_riscos/01_pendencias.md) |
| **H1 a H11, H-DR1 a H-DR14** | Hipótese de falha em aberto (H-DR13: prorrogação do lease contra promoção; H-DR14: partição assimétrica) | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) |
| **S1 a S7** | Spikes de medição (RTT, fsync, parse, libs) | [05_implementacao/06_testes_e_spikes](../05_implementacao/06_testes_e_spikes.md) |
| **Seções 5.x, 6.x, 9.x** | Numeração herdada do documento de arquitetura, preservada nos arquivos de `01_arquitetura/` | Mapa em [00_COMECE_AQUI](../00_COMECE_AQUI.md#mapa-de-seções-da-arquitetura) |

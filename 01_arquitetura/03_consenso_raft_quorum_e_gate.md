# Consenso: Raft, quórum Azure (learners + gate) e os dois logs

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 5. Qual Raft usar

### 5.1 O que o nosso problema exige do Raft
1. **Learners (sem voto)** que recebem o log: as 3 réplicas Azure.
2. **Commit só depois de maioria AWS E maioria Azure.** Isso **não existe** em nenhuma biblioteca pronta (ver 5.3).
3. PreVote e CheckQuorum (evitar eleição espúria e líder isolado), transferência de liderança (`TimeoutNow`, [ADR-013](../03_adr/ADR-013-transferencia-planejada-de-lideranca.md)), ReadIndex (leitura forte, que na nossa camada espera `min(ReadIndex, gate)`, ver 5.3.4).
4. Muitos shards, cada um com seu grupo, **compartilhando WAL e transporte** para ficar leve.
5. Controle do armazenamento (nosso WAL) e do transporte (gRPC).

### 5.2 Comparação

| Critério | **etcd-io/raft** (`go.etcd.io/raft/v3`) | **Dragonboat** (`lni/dragonboat`) | **hashicorp/raft** |
|---|---|---|---|
| Natureza | **Biblioteca**: só o algoritmo. Armazenamento, transporte e relógio são nossos. | Framework completo, multi-grupo, com transporte e log próprios. | Biblioteca com FSM e transporte embutidos, um grupo por instância. |
| Learners / não-votantes | Sim (learners) | Sim (non-voting, witness) | Sim (nonvoter) |
| PreVote, CheckQuorum | Sim | Sim | PreVote só em versões recentes (verificar) |
| Transferência de liderança, ReadIndex | Sim | Sim | Transferência sim; leitura linearizável menos direta |
| Joint consensus | Sim | Não (reconfiguração um a um) | Não |
| Multi-grupo | **Não** (fazemos) | **Sim**, com heartbeats coalescidos | Não prático (um grupo por instância) |
| Controle do WAL e do gate de commit | **Total** | Limitado (log e SM são abstrações do framework) | Médio |
| Memória/CPU | Mínimo; só o que usamos | Maior (LogDB, caches); configurável | Médio; mais goroutines por grupo |
| Maturidade | Muito alta (etcd, e base do Raft do CockroachDB) | Alta em desempenho; **ritmo de manutenção a verificar** | Alta (Consul, Nomad, Vault) |
| Esforço nosso | **Alto**: loop de `Ready`, snapshot, transporte, multi-grupo | Médio | Médio |

### 5.3 O quórum duplo não existe pronto — e o que fazer
> **Aprovado pelo Bruno (12/10):** Azure como learners, com o gate abaixo.


**Tentação a evitar:** usar *joint consensus* do etcd-io/raft para modelar "maioria AWS e maioria Azure" (config antiga = AWS, config nova = Azure, e nunca sair da fase conjunta). Funciona para o commit, mas:
- a **eleição** também passa a exigir maioria Azure, o que contradiz a opção B (AWS segue sozinha se a Azure cair);
- sair do modo degradado exigiria uma mudança de configuração que, pela regra conjunta, também precisa de maioria Azure: **impasse**.

**Recomendação (R4): learners + gate.**
- Azure Z1–Z3 são **learners**. O Raft decide commit e eleição só com os 3 votantes AWS.
- O progresso de cada learner é o `matchIndex` que o líder já enxerga no *progress* de cada seguidor, e **só chega pelas respostas do `AppendEntries`** (nunca por um canal paralelo). Ver 5.3.1.
- Definimos `azureMajorityIndex` = o maior índice que ≥ 2 dos 3 learners confirmaram **como durável** (fsync feito), **no termo atual**.
- **`gateIndex = min(raftCommitIndex, azureMajorityIndex)`**. O ACK ao cliente e o applier só avançam até `gateIndex`.
- **Modo degradado (opção B, com lease):** quando a Azure fica inalcançável por mais de `DEGRADE_AFTER` (2 s), o líder propõe uma entrada replicada `DEGRADE(epoch, fromIndex, validade D)`. A partir dela, `gateIndex = raftCommitIndex`, e as respostas saem marcadas como degradadas (RPO > 0 nesse intervalo). **A validade D limita esse modo no tempo** (5.3.2, [ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)), e o modo só existe nas instalações com `DEGRADED_POLICY=accept` (5.3.3, [ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)).
- **Saída do degradado:** só depois que os learners Azure alcançam o índice atual (lag abaixo de um limite, com histerese), o líder propõe `RESTORE(epoch)`. Entre `fromIndex` e `RESTORE`, as escritas ficam marcadas como "sem garantia Azure".

**Por que isso é melhor:** usa o Raft padrão (provado) para ordem, eleição e reconfiguração, e isola a nossa regra específica numa função de ~20 linhas que dá para modelar em **TLA+** e testar de forma determinística.

**O que continua sendo risco:**
1. O *gate* é lógica nossa, fora do Raft provado. Exige modelo TLA+ e testes de simulação (seção 13). O modelo é **ampliado** ([ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)) e nasce junto com a fase 1 (ver 5.3.5).
2. Entradas que o líder replicou para os learners mas que **nunca foram commitadas** podem existir na Azure. Se a Azure for promovida (por pessoa), o procedimento tem de escolher o log por (term, index), como numa eleição, e o cliente que recebeu `PENDING` deve tratar o resultado como desconhecido. Isso fica documentado e nas ferramentas de promoção.
3. **Promoção da Azure:** continua humana ([ADR-033](../03_adr/ADR-033-promocao-da-azure-humana.md)), mas agora com a regra de espera `D + M` e a recusa pela API (5.3.2). A PEND-01 passa de "sem proposta" para "desenho decidido"; fecha quando D e M forem aprovados e o modelo provar a ausência de escrita nas duas pontas.

**Gate monotônico (D9, Bruno, 12/10; refinado em 13/10, ADR-046).** `azureMajorityIndex` é estado do líder e nunca pode recuar. Ao assumir, o líder novo **não ACKa nem libera o applier** até reconstruir o valor, e só adota **progresso confirmado por ele mesmo no termo novo**, pelas respostas do `AppendEntries`. O que um learner reportou fora de banda (por exemplo, o sufixo de uma líder antiga) é **ignorado** até a primeira confirmação. O mesmo vale para o par DEGRADE/RESTORE. Este comportamento é a primeira propriedade do modelo TLA+ (D12).

### 5.3.1 Gate por termo e por evento (ADR-046)

| Regra | O que muda no desenho |
|---|---|
| **Progresso só por `AppendEntries`** | Um learner responde depois do fsync; o índice de casamento já prova "o mesmo prefixo até N" (casamento de logs do Raft). Nenhum canal paralelo de progresso |
| **`LearnerDurable()` devolve (termo, índice, identidade do learner)** | O gate só adota valores do **termo atual** da líder. Um número sem termo não prova nada |
| **Líder nova ignora o que não confirmou** | Valores reportados fora de banda são descartados até a primeira confirmação no termo novo. Janela curta sem ACK depois da eleição, já aceita no [ADR-029](../03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md) |
| **Gate alimentado por evento** | Cada resposta de learner atualiza o gate no momento em que chega, independentemente do fluxo de commits. Antes, o gate só consultava os learners ao processar um lote commitado, e uma escrita sem novo commit esperava até o timeout |
| **Troca de membros** | Disco substituído com identidade nova ([ADR-034](../03_adr/ADR-034-fsync-fatal-e-identidade-nova.md)) entra como learner novo; o progresso do antigo não é herdado |

**Nomes e métricas por shard.** Cada índice tem um nome e uma métrica, para nunca misturar "commitado" com "garantido":

| Nome | Significado |
|---|---|
| `last_committed` | Último índice commitado pelo Raft (maioria AWS) |
| `last_azure_durable` | `azureMajorityIndex`: 2º maior índice durável dos learners, no termo atual |
| `last_acked` | Último índice cuja escrita já foi respondida ao cliente (sempre menor ou igual ao gate) |
| `last_applied` | Último índice aplicado por inteiro no banco da célula (nunca passa do gate, [ADR-053](../03_adr/ADR-053-eventual-sem-apply-paralelo-na-v1.md)) |
| `last_reconciled` | Último índice verificado por checksum de faixa de chaves (anti-entropia, [ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)) |

Mais `lease_remaining` (tempo restante do modo degradado) e `poison_entry_index` (por item). A lista completa de métricas está em [10_pontos_adicionais_e_metricas](10_pontos_adicionais_e_metricas.md) (12.1).

### 5.3.2 Modo degradado com lease e promoção depois de D + M (ADR-044)

O problema nasce da opção B: com o link caído, a AWS segue escrevendo. Se uma pessoa promover a Azure nesse intervalo, as duas pontas aceitam escrita, e revogar credenciais ou checar a epoch no boot não ajuda, porque a ponta antiga não sabe que foi substituída. Sem terceiro domínio ([ADR-008](../03_adr/ADR-008-terceiro-dominio.md)), a autoridade única só pode vir do **tempo**.

1. **A entrada `DEGRADE` carrega uma validade D** (`DEGRADED_LEASE`). D é o teto de duração do RPO > 0 que o negócio precisa aprovar (PEND-02); **o valor é a definir**. O prazo conta no **relógio monotônico da líder** **desde o último contato com a maioria dos learners da Azure** (`t_c`: o instante de envio do último heartbeat ou `AppendEntries` que a maioria Azure confirmou) e **não desde a proposta** do `DEGRADE`. Assim a janela não depende de quando chega a primeira escrita depois da partição: se a proposta ocorrer horas depois de `t_c`, o lease já está vencido.
2. **A líder confere a validade a cada ACK**, depois do fsync e antes de responder, e não apenas em um temporizador. Pausa de processo (GC, VM congelada) é o caso perigoso; um salto de tempo detectado conta como lease expirada. **Uma líder nova que herda o `DEGRADE` não herda `t_c`** (e o nó reiniciado também não): trata o lease como expirado até obter contato com a Azure ou um `RENEW`.
3. **Ao fim de D a AWS para de aceitar escrita sozinha:** `503 LEASE_EXPIRED`. Leitura e Raft continuam. Só uma pessoa prorroga, por uma entrada replicada `RENEW(epoch, nova validade)` no Raft da AWS.
4. **A Azure só pode ser promovida depois de D + M**, contados no relógio monotônico dela a partir do **último contato recebido da AWS**. `M = deriva máxima de relógio + pausa máxima de processo` (valores a definir). `DEGRADE_AFTER` não entra em M: a AWS conta D desde `t_c`, confirmado por uma maioria Azure, e duas maiorias de 3 learners se cruzam em ao menos um learner, que viu a AWS em `t_c` ou depois; a Azure não fica apta a promover antes de a AWS ter parado. A API `ForcePromote` **recusa** antes desse prazo. A promoção continua humana.
5. **Partição antes do `DEGRADE`** (os primeiros 2 s) segue a mesma regra de espera para promover: o lease já corre desde `t_c`.
6. **Custo:** se a AWS morre de verdade em modo degradado, o **RTO passa a ser pelo menos D + M**. É o preço de ter autoridade única sem terceiro domínio. Ao fim de D as escritas param mesmo que a Azure ainda esteja inalcançável: a disponibilidade em modo degradado é finita.
7. **Hipóteses abertas, para o TLA+:** prorrogar depois que a Azure já pode ser promovida reabre o risco (H-DR13; a regra, por exemplo marcar a Azure como `promotion_blocked` antes de prorrogar, fica para o modelo); partição assimétrica (H-DR14). Ambas na PEND-20. As regras de `t_c` e da líder nova são decididas e entram no modelo para prova. Não há contorno de emergência (break-glass) para a recusa do `ForcePromote` na v1 (decidido pelo Bruno em 13/10, 15b).

Cenário: [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md). Estados: [D06](../04_diagramas/D06-estados-do-shard-e-do-no.md). Prazos: [D07](../04_diagramas/D07-prazos-em-cascata.md).

### 5.3.3 DEGRADED_POLICY por instalação (ADR-045)

Variável **obrigatória, sem valor padrão**, validada na partida: `DEGRADED_POLICY=accept` ou `reject`.

| Valor | Comportamento | RPO |
|---|---|---|
| `accept` | Vale o modo degradado da opção B, limitado pelo lease (5.3.2), com o header `x-dr-mode: degraded` e a métrica. Exige o aceite formal do RPO > 0 e do valor de D (PEND-02). Exige também `DEGRADED_LEASE` | > 0 durante o episódio |
| `reject` | A líder **nunca propõe `DEGRADE`**. Sem o gate da Azure a escrita não é ACKada: o cliente recebe `503 PENDING` (resultado desconhecido). Não há lease. A promoção se apoia na maioria Azure já estar na epoch nova (fencing por maioria, a provar no modelo) | 0 sempre |

A recomendação para dados monetários que não aceitam RPO > 0 é `reject`. O header, sozinho, não é aceite de risco: os SDKs o ignoram.

### 5.3.4 Barreira da leitura forte: `min(ReadIndex, gate)` (ADR-049)

A leitura forte esperava `applied >= ReadIndex`. Como o apply só ocorre depois do gate, uma Azure lenta atrasava as leituras fortes mesmo sem escrita pendente. A correção: um cliente só pode ter visto escritas já ACKadas, e toda escrita ACKada está abaixo do gate; logo a barreira é **`min(ReadIndex, gate)`**.

- Na célula follower, a líder devolve o `ReadIndex` e o gate **na mesma resposta**; um gate antigo guardado na follower enfraqueceria a garantia.
- A líder recém-eleita só atende leitura forte **depois de reconstruir o gate** (5.3, ADR-029, ADR-046).
- Com o applier parado por entrada venenosa, a leitura forte do shard **falha rápido com `503`**, em vez de esperar até o timeout.
- Depende de `applied` nunca passar do gate (apply serial depois do gate, ADR-053).

Detalhe: [06_modos_de_escrita_e_leitura_e_latencia](06_modos_de_escrita_e_leitura_e_latencia.md) (7.2) e [R16](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md).

### 5.3.5 Modelo TLA+ ampliado (ADR-046, ADR-030)

O modelo cobre, além do gate monotônico: promoção entre nuvens com lease, troca de membros (disco substituído, identidade nova), dedupe, resultado desconhecido, DELETE e replay (I-DEL). Ele **nasce junto com a fase 1** do plano, porque o lease só vale se for modelado, e o núcleo mínimo verificável vem **antes** de qualquer caminho de banco.

### 5.4 Recomendação e plano B

**Decisão (12/10): `etcd-io/raft`.** O Bruno delegou a escolha entre `etcd-io/raft` e Dragonboat pelos critérios de desempenho, segurança, estabilidade e comunidade em evolução:
- **Segurança e estabilidade:** o `etcd-io/raft` é a base do etcd (o armazenamento do Kubernetes) e foi adotado por outros bancos distribuídos; é uma biblioteca pequena e muito exercitada, com a exatidão do algoritmo como foco.
- **Comunidade:** mantido pela comunidade do etcd, ligada ao ecossistema do Kubernetes. O Dragonboat é muito bom em vazão, mas, pelo que sei, depende de poucos mantenedores e teve ritmo menor de evolução recentemente. **Verificar a atividade dos dois repositórios no spike S6 antes de fixar a versão.**
- **Desempenho:** o Dragonboat se destaca em vazão com muitos grupos. Com poucos shards e ~3.000 TPS, o limite é o fsync e o RTT, não o motor Raft, então a vantagem dele não pesa aqui.
- **Encaixe com as decisões já tomadas:** com dois logs (5.5.1, armazenamento revisto no ADR-055) e o gate (5.3), precisamos controlar armazenamento e transporte. O `etcd-io/raft` deixa isso conosco; o Dragonboat traz o log e o transporte dele.
- **Preço:** escrever o laço de `Ready`, o transporte gRPC e a camada multi-grupo (pequena, por haver poucos grupos).


- **Escolha:** `etcd-io/raft` + camada multi-grupo nossa (um *event loop* por conjunto de shards, não uma goroutine pesada por shard; heartbeats coalescidos por par de nós; WAL e transporte compartilhados).
- **Por quê:** é a única opção que nos deixa implementar o gate, o WAL compartilhado e o transporte gRPC sem forkar. E é a que tem menos peso de memória.
- **Preço:** escrever e testar a camada multi-grupo (é o item mais caro da v1). Dragonboat já entrega isso.
- **Plano B (Dragonboat):** se A3 for falsa (milhares de shards) ou se o spike mostrar que a camada multi-grupo estoura o prazo. Custo: o gate teria de viver na máquina de estados e dependeríamos de ler o progresso dos learners por fora do framework; a reconfiguração precisa ser revista.
- **Descartado:** hashicorp/raft (um grupo por instância não escala em memória; menos controle do commit). Implementar Raft do zero (risco desnecessário).

---

### 5.5 Isolar o Raft do log de replicação (requisito do Bruno, 12/10)

> **Decisão do Bruno (12/10): dois logs separados** — o **log do Raft** e o **log de replicação** (nosso). Esta é a opção L2 da tabela abaixo. O desenho que a implementa sem custo no caminho do ACK está em **5.5.1**.

**Requisito:** o log de replicação não pode depender da biblioteca Raft escolhida, para permitir trocar ou evoluir o consenso sem reescrever o resto.

**Dois níveis possíveis de separação:**

| Nível | O que é | Custo | Recomendação |
|---|---|---|---|
| **L1. Lógico** | Um só armazenamento físico, mas o formato do registro e a interface são **nossos**. O Raft vê o log por um adaptador. | Baixo | Era a proposta; **substituída pela decisão L2** |
| **L2. Físico** | Dois logs: o **log de dados** (post-images, nosso formato) e o log do Raft, que só carrega referências de ordenação (shard, índice, hash, posição no log de dados). | Alto: 2 gravações duráveis no caminho do ACK, e a réplica dos dados precisa de um canal próprio, o que é reescrever parte da replicação | **Decidido pelo Bruno (12/10)**, com o desenho de 5.5.1, que evita o segundo fsync no caminho do ACK |

**Interfaces e regras que continuam valendo (vindas do L1):**
1. **Registro próprio.** Cada entrada do log é um registro DataReplication (6.3) com `term` e `index` no cabeçalho, mais o payload opaco. Nenhum tipo da biblioteca (`raftpb` etc.) vai para o disco nem para fora do adaptador.
2. **Interface `ReplicationLog`** (do `core`): anexar em lote, ler por intervalo, truncar sufixo, compactar prefixo, índice durável. É o que o applier, a ressincronização da Azure e o `drctl` usam. **Nenhum deles passa pelo Raft para ler dado.**
3. **Interface `ConsensusEngine`** (do `core`): propor, índice commitado (eventos), liderança e transferência, `ReadIndex`, membros e progresso dos learners (`LearnerDurable()` devolve termo, índice e identidade, ADR-046). O gate (5.3) usa só essa interface.
4. **Adaptador `consensus/etcdraft`.** É o **único pacote** que importa `go.etcd.io/raft`. Traduz o `ReplicationLog` para o que a biblioteca espera de armazenamento e guarda separadamente o estado próprio dela (term, voto, configuração) num `ConsensusStateStore`. Uma regra de lint (ex.: `depguard`) impede importar a biblioteca fora do adaptador.
5. **Suíte de conformidade.** Os mesmos testes (simulação de falhas, linearizabilidade, queda no WAL) rodam contra a interface, não contra a biblioteca. Trocar o Raft = escrever outro adaptador e passar na mesma suíte.

**O que ainda acopla (e é aceito):** `term` e `index` por entrada e a noção de líder, que qualquer Raft ou Multi-Paxos tem. Um protocolo sem esses conceitos exigiria o L2.

**Tradeoff do L2 (registrado quando a decisão foi tomada):** separar fisicamente pareceria mais limpo, mas obriga a gravar os dados e a referência de ordem com durabilidade (dados primeiro, referência depois, para nunca haver referência sem dado) e a replicar o dado por um canal que também precisa de quórum. Em troca de uma flexibilidade que o L1 já dá quase toda, pagaríamos latência no orçamento de 50ms.

### 5.5.1 Desenho dos dois logs (decisão do Bruno, 12/10; armazenamento revisto em 13/10)

> **Revisão de 13/10 ([ADR-055](../03_adr/ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)).** Log do Raft no pacote `wal` do etcd, um por shard; log de replicação no **Pebble**, derivado do Raft, **sem fsync**, com checkpoint com sync periódico, em **volume separado**. A regra 1 abaixo passa a ler: o Raft só é compactado até o **`DurableIndex` do Pebble, que é o índice coberto por um checkpoint com sync**. A retenção longa para a Azure fica no Pebble; o Raft guarda só a folga `RAFT_RETAIN_MAX` ([04_wal_e_fsync](04_wal_e_fsync.md), 6.9).

**Ideia:** o **log do Raft** é interno ao consenso e de vida curta; o **log de replicação** é nosso, de formato próprio, de vida longa, independente da biblioteca Raft.

| | Log do Raft | Log de replicação |
|---|---|---|
| **Dono** | A biblioteca Raft (pelo adaptador) | O `DataReplication` |
| **Conteúdo** | Entradas e estado do consenso (term, voto, configuração), com o payload para poder replicar | Registros de escrita já **commitados**, no nosso formato (post-image, versão, nome lógico da tabela) |
| **Quando é gravado** | Antes de responder ao líder (caminho do ACK, fsync obrigatório) | **Depois do commit**, na ordem do log, sem fsync; checkpoint com sync periódico (ADR-055) |
| **Quem lê** | Só o Raft | Applier, ressincronização da Azure, `drctl`, reconstrução de réplica |
| **Retenção** | Curta: até o `DurableIndex` do Pebble, mais a folga `RAFT_RETAIN_MAX` | Longa: pela janela de tolerância da Azure e da manutenção (6.5), teto de 70% |
| **Armazenamento (13/10)** | Pacote `wal` do etcd, um por shard, volume pequeno e rápido | Pebble, volume próprio |
| **Se trocarmos o Raft** | Muda | **Não muda** |

**Por que não adiciona latência ao ACK:** o payload já foi gravado com fsync no log do Raft antes do commit. O applier consome as entradas commitadas **da memória**, e a gravação no log de replicação segue em paralelo, em lote. Duas regras mantêm a segurança:
1. **Só se trunca o log do Raft até o que já está durável no log de replicação.**
2. **Na partida**, se o log de replicação estiver atrás do índice commitado, ele é completado a partir do log do Raft antes de aceitar tráfego.

**Custos e riscos:**
- **Escrita em dobro** em cada réplica. A ~6 MB/s é pouco volume. Desde o ADR-055 o segundo log não faz fsync por entrada e fica em **volume separado**, então não disputa a cauda do fsync do log do Raft (6.8); a compactação do Pebble fica no volume dele.
- **Duas coisas para manter consistentes**: o índice do log de replicação é o próprio índice Raft (sem sequência paralela), e a suíte de conformidade (5.5) passa a testar também a recuperação nos casos de queda entre os dois logs.
- **O gate (5.3) não muda**: a durabilidade que conta para o ACK é a do log do Raft.
- **Interface `ReplicationLog` (5.5)**: passa a ser a interface do log de replicação, e o adaptador do Raft recebe os registros commitados por um callback.

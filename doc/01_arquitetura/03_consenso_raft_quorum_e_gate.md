# Consenso: Raft, quórum Azure (learners + gate) e o log do Raft

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
| `last_applied` | Último índice aplicado por inteiro no banco da célula (nunca passa do gate, [ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md)) |
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
- Depende de `applied` nunca passar do gate (apply só despachado depois do gate, ADR-057; `applied` é a marca d'água contígua, ADR-058).

Detalhe: [06_leitura_forte_escrita_serial_e_latencia](06_leitura_forte_escrita_serial_e_latencia.md) (7.2) e [R16](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md).

### 5.3.5 Modelo TLA+ ampliado (ADR-046, ADR-030)

O modelo cobre, além do gate monotônico: promoção entre nuvens com lease, troca de membros (disco substituído, identidade nova), dedupe, resultado desconhecido, DELETE e replay (I-DEL) e, desde o ADR-056, log volátil x durável por nó com queda em qualquer passo e a invariante "toda entrada ACKada está no log durável de 2 nós AWS e 2 nós Azure" (5.5.2). Ele **nasce junto com a fase 1** do plano, porque o lease só vale se for modelado, e o núcleo mínimo verificável vem **antes** de qualquer caminho de banco.

### 5.4 Recomendação e plano B

**Decisão (12/10): `etcd-io/raft`.** O Bruno delegou a escolha entre `etcd-io/raft` e Dragonboat pelos critérios de desempenho, segurança, estabilidade e comunidade em evolução:
- **Segurança e estabilidade:** o `etcd-io/raft` é a base do etcd (o armazenamento do Kubernetes) e foi adotado por outros bancos distribuídos; é uma biblioteca pequena e muito exercitada, com a exatidão do algoritmo como foco.
- **Comunidade:** mantido pela comunidade do etcd, ligada ao ecossistema do Kubernetes. O Dragonboat é muito bom em vazão, mas, pelo que sei, depende de poucos mantenedores e teve ritmo menor de evolução recentemente. **Verificar a atividade dos dois repositórios no spike S6 antes de fixar a versão.**
- **Desempenho:** o Dragonboat se destaca em vazão com muitos grupos. Com poucos shards e ~3.000 TPS, o limite é o fsync e o RTT, não o motor Raft, então a vantagem dele não pesa aqui.
- **Encaixe com as decisões já tomadas:** com o log do Raft como único log do nó (5.5.1, armazenamento revisto em 13/10) e o gate (5.3), precisamos controlar armazenamento e transporte. O `etcd-io/raft` deixa isso conosco; o Dragonboat traz o log e o transporte dele.
- **Preço:** escrever o laço de `Ready`, o transporte gRPC e a camada multi-grupo (pequena, por haver poucos grupos).


- **Escolha:** `etcd-io/raft` + camada multi-grupo nossa (um *event loop* por conjunto de shards, não uma goroutine pesada por shard; heartbeats coalescidos por par de nós; WAL e transporte compartilhados).
- **Por quê:** é a única opção que nos deixa implementar o gate, o WAL compartilhado e o transporte gRPC sem forkar. E é a que tem menos peso de memória.
- **Preço:** escrever e testar a camada multi-grupo (é o item mais caro da v1). Dragonboat já entrega isso.
- **Plano B (Dragonboat):** se A3 for falsa (milhares de shards) ou se o spike mostrar que a camada multi-grupo estoura o prazo. Custo: o gate teria de viver na máquina de estados e dependeríamos de ler o progresso dos learners por fora do framework; a reconfiguração precisa ser revista.
- **Descartado:** hashicorp/raft (um grupo por instância não escala em memória; menos controle do commit). Implementar Raft do zero (risco desnecessário).

---

### 5.5 Isolar o Raft do resto do sistema (requisito do Bruno, 12/10)

> **Decisão (12/10, armazenamento revisto em 13/10):** um **único log por nó, o log do Raft**, com separação **lógica** (opção L1 da tabela abaixo): o formato do registro e as interfaces são nossos e só o adaptador conhece a biblioteca. O log do Raft é a única fonte das entradas commitadas. O desenho está em **5.5.1**.

**Requisito:** o applier, o gate, o `drctl` e a reconstrução não podem depender da biblioteca Raft escolhida, para permitir trocar ou evoluir o consenso sem reescrever o resto.

**Dois níveis possíveis de separação:**

| Nível | O que é | Custo | Recomendação |
|---|---|---|---|
| **L1. Lógico** | Um só armazenamento físico (o log do Raft), mas o formato do registro e as interfaces são **nossos**. O resto do sistema vê o Raft por um adaptador. | Baixo | **Decidido**, com o desenho de 5.5.1 |
| **L2. Físico** | Um **log de dados** próprio (post-images, nosso formato) separado do log do Raft, que só carregaria referências de ordenação (shard, índice, hash, posição no log de dados). | Alto: 2 gravações duráveis no caminho do ACK, e a réplica dos dados precisaria de um canal próprio, o que é reescrever parte da replicação | Descartado |

**Interfaces e regras:**
1. **Registro próprio.** O payload de cada entrada do Raft é um registro DataReplication (6.3: post-image, versão, nome lógico da tabela, itens com ordem), opaco para a biblioteca. Nenhum tipo da biblioteca (`raftpb` etc.) sai do adaptador.
2. **Entrega das entradas commitadas.** O adaptador publica o índice commitado como sinal, e o `CommitPump` lê as entradas commitadas por faixa da memória do Raft e as entrega ao Applier ([ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)). O applier, a reconstrução e o `drctl` recebem entradas no nosso formato; nenhum deles importa a biblioteca.
3. **Interface `ConsensusEngine`** (do `core`): propor, índice commitado (eventos; entradas já estáveis no disco da própria líder, ADR-056), liderança e transferência, `ReadIndex`, membros e progresso dos learners (`LearnerDurable()` devolve termo, índice e identidade, ADR-046). O gate (5.3) usa só essa interface.
4. **Adaptador `consensus/etcdraft`.** É o **único pacote** que importa `go.etcd.io/raft`. Guarda as entradas e o estado próprio da biblioteca (term, voto, configuração) no `wal` do etcd, um por shard, no volume do Raft, e compacta o log pela regra `domain.RaftCompactLimit` ([04_wal_e_fsync](04_wal_e_fsync.md), 6.9). Uma regra de lint (ex.: `depguard`) impede importar a biblioteca fora do adaptador.
5. **Suíte de conformidade.** Os mesmos testes (simulação de falhas, linearizabilidade, queda no WAL) rodam contra a interface, não contra a biblioteca. Trocar o Raft = escrever outro adaptador e passar na mesma suíte.

**O que ainda acopla (e é aceito):** `term` e `index` por entrada e a noção de líder, que qualquer Raft ou Multi-Paxos tem, e o formato em disco do log, que é o da biblioteca. Trocar de biblioteca troca também esse formato; o nó migra por reconstrução (identidade nova, ADR-034), não por conversão do arquivo. Um protocolo sem `term` e `index` exigiria o L2.

**Por que não o L2:** separar fisicamente pareceria mais limpo, mas obriga a gravar os dados e a referência de ordem com durabilidade (dados primeiro, referência depois, para nunca haver referência sem dado) e a replicar o dado por um canal que também precisa de quórum. Em troca de uma flexibilidade que o L1 já dá quase toda, pagaríamos latência no orçamento de 50ms.

### 5.5.1 Desenho do log do Raft como único log do nó (decisão do Bruno, 12/10; armazenamento revisto em 13/10)

> **Armazenamento (13/10).** Log do Raft no pacote `wal` do etcd, um por shard, com fsync antes do ACK e group commit por shard ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)), num **volume de bloco dedicado por nó** ([ADR-019](../03_adr/ADR-019-wal-proprio-volume-dedicado.md)). A retenção para a Azure atrasada fica no próprio log do Raft, e a compactação segue a regra `RaftCompactLimit` ([04_wal_e_fsync](04_wal_e_fsync.md), 6.9).

**Ideia:** o **log do Raft** é o único log do nó. Ele carrega o payload no nosso formato, é replicado pelo próprio Raft para seguidores e learners e é a única fonte das entradas que o applier grava no banco.

| | Log do Raft |
|---|---|
| **Dono** | A biblioteca Raft (pelo adaptador) |
| **Conteúdo** | Entradas e estado do consenso (term, voto, configuração); o payload de cada entrada é um registro nosso (post-image, versão, nome lógico da tabela) |
| **Quando é gravado** | Antes de responder ao líder (caminho do ACK, fsync obrigatório, group commit por shard) |
| **Quem lê** | O Raft (inclusive o `MsgApp` para seguidores e learners atrasados) e o `CommitPump`, que entrega as entradas commitadas ao Applier; a reconstrução em tabela sombra reaplica as entradas a partir de F |
| **Retenção** | Compactado só até `RaftCompactLimit` = `min(aplicado local, índice durável da maioria Azure enquanto a Azure estiver na janela, F − 1 de uma reconstrução em curso)`, com folga até o menor `match` dos votantes AWS limitada por `RAFT_RETAIN_MAX`; teto de 70% do volume do Raft (6.5, 6.9). Na POC a compactação está desligada (RT-11) |
| **Armazenamento** | Pacote `wal` do etcd, um por shard, num volume de bloco dedicado por nó |
| **Se trocarmos o Raft** | Muda o adaptador e o formato em disco; o applier, o gate e o resto **não mudam** |

**Por que não adiciona latência ao ACK:** o payload é gravado com fsync no log do Raft antes do commit, e esse é o único fsync no caminho do ACK. O applier consome as entradas commitadas **da memória** do Raft, e o progresso dele (índice e ordem da marca d'água) é um registro pequeno com CRC, sem fsync, no volume do Raft. Duas regras mantêm a segurança:
1. **Só se compacta o log do Raft até `RaftCompactLimit`.** O aplicado local já está no banco da célula, então compactar até ele é seguro; o índice durável da maioria Azure e o F − 1 de uma reconstrução seguram o que eles ainda precisam.
2. **Na partida**, a retomada é o índice do progresso do applier (sem progresso, 1); o Raft reinicia com `Applied = retomada − 1`, limitado entre o snapshot e o commit gravado, e o replay vem do WAL do Raft antes de aceitar tráfego ([ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md), item 5).

**Custos e riscos:**
- **Um volume por nó e nenhuma escrita em dobro.** O único fsync no caminho do ACK é o do Raft (6.8).
- **Retenção na memória.** O `MemoryStorage` do etcd-raft guarda em RAM tudo o que não foi compactado; num modo degradado longo a memória do nó cresce com o atraso da Azure até o teto. Mitigações em aberto: teto da janela também por bytes retidos em memória, ou um `raft.Storage` que lê as entradas antigas do WAL em disco. Medir no spike S1.
- **Perder o progresso do applier** só alonga o replay, que é idempotente pela versão.
- **O gate (5.3) não muda**: a durabilidade que conta para o ACK é a do log do Raft.

### 5.5.2 Escrita do log do Raft: group commit por shard e nada sai antes do fsync (ADR-056, 13/10)

> **Decisão do Bruno (13/10, respostas 1a 2a 3b 4a 5a 6a).** Detalhe em [ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md) e em [04_wal_e_fsync](04_wal_e_fsync.md) (6.10).

**Invariante:** nenhum ACK ao cliente, nenhum voto (`MsgVoteResp`) e nenhuma resposta de `AppendEntries` (`MsgAppResp`) sai antes do fsync que cobre a entrada, em nenhum dos 6 nós. A líder só se conta no quórum depois do próprio fsync. O buffer em memória é só a área de montagem do lote.

**Modo da biblioteca na fase 1: síncrono com F3.**

| Mensagem do `Ready` | Pode sair antes do fsync do lote? | Motivo |
|---|---|---|
| `MsgApp` da líder para seguidores e learners | **Sim** (F3, seção 10.2.1 da tese) | O par só responde depois do próprio fsync; a líder só se conta depois do dela |
| `MsgAppResp` (seguidor AWS ou learner Azure) | **Não** | O gate e o commit contariam durabilidade que não existe |
| `MsgVoteResp`, `MsgPreVoteResp` | **Não** | O voto precisa estar no `HardState` durável |
| Heartbeat e demais | **Não** no modo síncrono | Regra simples e única; o ganho de antecipar é nenhum |

A regra está em `domain.CanSendBeforeSync` no [código de exemplo](../05_implementacao/codigo_exemplo/README.md). `Advance` só é chamado depois do fsync.

**Interação com o resto do consenso:**
- **Gate** (5.3, 5.3.1): o progresso dos learners vem só do `MsgAppResp` no termo atual, que só sai depois do fsync do learner. O commit da AWS pode ser alcançado por dois seguidores enquanto o disco da líder grava: continua 2 de 3 com fsync.
- **ReadIndex e barreira `min(ReadIndex, gate)`** (5.3.4): o ReadIndex depende de heartbeat com quórum, não do disco. O commit da líder pode estar acima do disco dela, mas a leitura espera `applied`, que só anda com entrada estável na líder.
- **Eleição e transferência** (ADR-013): o voto só sai depois de termo e voto estarem no disco; a eleição fica um fsync mais lenta.
- **Fsync lento no modo síncrono** congela o laço do shard: sem heartbeats, seguidores podem iniciar eleição e o processamento das respostas da Azure atrasa, o que pode fazer o lease do modo degradado (5.3.2) parecer vencido. Atenuação: `ElectionTick` bem acima do P99.9 de fsync (configurável em `ELECTION_TICKS`, 30 na POC) e F7 (transferir a liderança com fsync lento). O spike S1b mede se o `AsyncStorageWrites` elimina isso.
- **Apply lento não congela mais o laço ([ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)).** Antes, o laço do `Ready` entregava os commits por um canal bloqueante, e um banco lento numa célula parava o tick e o heartbeat e derrubava a liderança do grupo (teste de carga de 14/10). Agora o laço só publica o índice commitado (atômico mais aviso) e chama `Advance`; o `CommitPump` lê as entradas por faixa do `MemoryStorage`, e o gate segue o commit numa goroutine própria. Métricas do laço: `dr_raft_loop_duration_seconds` e `dr_raft_tick_gap_seconds`. Ver [C35](../02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md).

**`AsyncStorageWrites`: critério de entrada (spike S1b).** Entra se reduzir o P99 do commit em pelo menos 20% **ou** eliminar eleições causadas por fsync lento (atraso de disco injetado de 50 a 500 ms). No modo assíncrono a biblioteca já prende `MsgAppResp`, votos e o auto-ACK da líder à escrita (`msgsAfterAppend`) e só entrega à aplicação entradas estáveis localmente. A troca fica restrita ao adaptador `etcdraft`. Não há variável para escolher o modo em produção.

**Contrapressão:** `MaxUncommittedEntriesSize` de 32 MiB por shard e `MaxInflightBytes` configurado; acima disso a proposta é recusada com `ThrottlingException`, que os SDKs repetem, e o dedupe absorve a repetição. Desde o [ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md) há também a **admissão por atraso do apply**: antes do `Propose`, a líder recusa quando `gate − aplicado > APPLY_LAG_MAX` (padrão `APPLY_WORKERS × 256`). Toda recusa leva uma causa em `dr_throttled_total{causa}`: `buffer_cheio`, `nao_lider`, `transferencia` ou `atraso_apply`.

**TLA+ (ADR-030):** o modelo separa log volátil e durável por nó, com queda em qualquer passo, e verifica que toda entrada ACKada está no log durável de 2 nós AWS e 2 nós Azure, nos dois modos.


# Consenso: Raft, quórum Azure (learners + gate) e os dois logs

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 5. Qual Raft usar

### 5.1 O que o nosso problema exige do Raft
1. **Learners (sem voto)** que recebem o log: as 3 réplicas Azure.
2. **Commit só depois de maioria AWS E maioria Azure.** Isso **não existe** em nenhuma biblioteca pronta (ver 5.3).
3. PreVote e CheckQuorum (evitar eleição espúria e líder isolado), transferência de liderança (`TimeoutNow`, [ADR-013](../03_adr/ADR-013-transferencia-planejada-de-lideranca.md)), ReadIndex (leitura forte).
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
- Cada learner informa o `matchIndex` (o líder já o enxerga no *progress* de cada seguidor).
- Definimos `azureMajorityIndex` = o maior índice que ≥ 2 dos 3 learners confirmaram **como durável** (fsync feito).
- **`gateIndex = min(raftCommitIndex, azureMajorityIndex)`**. O ACK ao cliente e o applier só avançam até `gateIndex`.
- **Modo degradado (opção B):** quando a Azure fica inalcançável por mais de N segundos, o líder propõe uma entrada replicada `DEGRADE(epoch, fromIndex)`. A partir dela, `gateIndex = raftCommitIndex`, e as respostas saem marcadas como degradadas (RPO > 0 nesse intervalo).
- **Saída do degradado:** só depois que os learners Azure alcançam o índice atual (lag abaixo de um limite, com histerese), o líder propõe `RESTORE(epoch)`. Entre `fromIndex` e `RESTORE`, as escritas ficam marcadas como "sem garantia Azure".

**Por que isso é melhor:** usa o Raft padrão (provado) para ordem, eleição e reconfiguração, e isola a nossa regra específica numa função de ~20 linhas que dá para modelar em **TLA+** e testar de forma determinística.

**O que continua sendo risco:**
1. O *gate* é lógica nossa, fora do Raft provado. Exige modelo TLA+ e testes de simulação (seção 13).
2. Entradas que o líder replicou para os learners mas que **nunca foram commitadas** podem existir na Azure. Se a Azure for promovida sozinha (PEND-01, por pessoa), o procedimento tem de escolher o log por (term, index), como numa eleição, e o cliente que recebeu `PENDING` deve tratar o resultado como desconhecido. Isso fica documentado e nas ferramentas de promoção, **fora do escopo do código do v1**.
3. Promoção da Azure continua humana e fora deste documento (PEND-01).

**Gate monotônico (D9, Bruno, 12/10).** `azureMajorityIndex` é estado do líder e nunca pode recuar. Ao assumir, o líder novo **não ACKa nem libera o applier** até reconstruir o valor consultando os learners da Azure (índice durável de cada um) e adotar o maior índice confirmado por ≥ 2 de 3. O mesmo vale para o par DEGRADE/RESTORE. Este comportamento é a primeira propriedade do modelo TLA+ (D12).

### 5.4 Recomendação e plano B

**Decisão (12/10): `etcd-io/raft`.** O Bruno delegou a escolha entre `etcd-io/raft` e Dragonboat pelos critérios de desempenho, segurança, estabilidade e comunidade em evolução:
- **Segurança e estabilidade:** o `etcd-io/raft` é a base do etcd (o armazenamento do Kubernetes) e foi adotado por outros bancos distribuídos; é uma biblioteca pequena e muito exercitada, com a exatidão do algoritmo como foco.
- **Comunidade:** mantido pela comunidade do etcd, ligada ao ecossistema do Kubernetes. O Dragonboat é muito bom em vazão, mas, pelo que sei, depende de poucos mantenedores e teve ritmo menor de evolução recentemente. **Verificar a atividade dos dois repositórios no spike S6 antes de fixar a versão.**
- **Desempenho:** o Dragonboat se destaca em vazão com muitos grupos. Com poucos shards e ~3.000 TPS, o limite é o fsync e o RTT, não o motor Raft, então a vantagem dele não pesa aqui.
- **Encaixe com as decisões já tomadas:** com WAL próprio, dois logs (5.5.1) e o gate (5.3), precisamos controlar armazenamento e transporte. O `etcd-io/raft` deixa isso conosco; o Dragonboat traz o log e o transporte dele.
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
3. **Interface `ConsensusEngine`** (do `core`): propor, índice commitado (eventos), liderança e transferência, `ReadIndex`, membros e progresso dos learners. O gate (5.3) usa só essa interface.
4. **Adaptador `consensus/etcdraft`.** É o **único pacote** que importa `go.etcd.io/raft`. Traduz o `ReplicationLog` para o que a biblioteca espera de armazenamento e guarda separadamente o estado próprio dela (term, voto, configuração) num `ConsensusStateStore`. Uma regra de lint (ex.: `depguard`) impede importar a biblioteca fora do adaptador.
5. **Suíte de conformidade.** Os mesmos testes (simulação de falhas, linearizabilidade, queda no WAL) rodam contra a interface, não contra a biblioteca. Trocar o Raft = escrever outro adaptador e passar na mesma suíte.

**O que ainda acopla (e é aceito):** `term` e `index` por entrada e a noção de líder, que qualquer Raft ou Multi-Paxos tem. Um protocolo sem esses conceitos exigiria o L2.

**Tradeoff do L2 (registrado quando a decisão foi tomada):** separar fisicamente pareceria mais limpo, mas obriga a gravar os dados e a referência de ordem com durabilidade (dados primeiro, referência depois, para nunca haver referência sem dado) e a replicar o dado por um canal que também precisa de quórum. Em troca de uma flexibilidade que o L1 já dá quase toda, pagaríamos latência no orçamento de 50ms.

### 5.5.1 Desenho dos dois logs (decisão do Bruno, 12/10)

**Ideia:** o **log do Raft** é interno ao consenso e de vida curta; o **log de replicação** é nosso, de formato próprio, de vida longa, independente da biblioteca Raft.

| | Log do Raft | Log de replicação |
|---|---|---|
| **Dono** | A biblioteca Raft (pelo adaptador) | O `DataReplication` |
| **Conteúdo** | Entradas e estado do consenso (term, voto, configuração), com o payload para poder replicar | Registros de escrita já **commitados**, no nosso formato (post-image, versão, nome lógico da tabela) |
| **Quando é gravado** | Antes de responder ao líder (caminho do ACK, fsync obrigatório) | **Depois do commit**, na ordem do log, em lote |
| **Quem lê** | Só o Raft | Applier, ressincronização da Azure, `drctl`, reconstrução de réplica |
| **Retenção** | Curta: até o marcador de compactação | Longa: pela janela de tolerância da Azure e da manutenção (6.5) |
| **Se trocarmos o Raft** | Muda | **Não muda** |

**Por que não adiciona latência ao ACK:** o payload já foi gravado com fsync no log do Raft antes do commit. O applier consome as entradas commitadas **da memória**, e a gravação no log de replicação segue em paralelo, em lote. Duas regras mantêm a segurança:
1. **Só se trunca o log do Raft até o que já está durável no log de replicação.**
2. **Na partida**, se o log de replicação estiver atrás do índice commitado, ele é completado a partir do log do Raft antes de aceitar tráfego.

**Custos e riscos:**
- **Escrita em dobro** em cada réplica. A ~6 MB/s é pouco volume, mas o segundo fsync disputa o mesmo volume e pode piorar a **cauda do fsync do log do Raft** (6.8). Mitigação: lote maior e menos frequente no log de replicação (ele não está no caminho do ACK) e, se a medição mostrar interferência, **volume separado** para cada log.
- **Duas coisas para manter consistentes**: o índice do log de replicação é o próprio índice Raft (sem sequência paralela), e a suíte de conformidade (5.5) passa a testar também a recuperação nos casos de queda entre os dois logs.
- **O gate (5.3) não muda**: a durabilidade que conta para o ACK é a do log do Raft.
- **Interface `ReplicationLog` (5.5)**: passa a ser a interface do log de replicação, e o adaptador do Raft recebe os registros commitados por um callback.

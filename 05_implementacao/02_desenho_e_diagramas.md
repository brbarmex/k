# Desenho da arquitetura e diagramas de processo e de fluxo

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice da implementação](README.md)

## 4. Desenho da arquitetura

```mermaid
flowchart LR
    subgraph IN["Adaptadores de entrada"]
        H["dynamohttp<br/>fasthttp"]
        PG["peergrpc<br/>gRPC"]
        AD["adminhttp<br/>net/http"]
    end
    subgraph CORE["Núcleo"]
        direction TB
        PIN["port in<br/>Writer, Reader"]
        UC["usecase<br/>write, read, apply,<br/>degrade, recovery"]
        DM["domain<br/>Entry, Gate, Mode"]
        POUT["port out<br/>ReplicationLog, ConsensusEngine,<br/>StateStore, Forwarder"]
        PIN --> UC --> DM
        UC --> POUT
    end
    subgraph OUT["Adaptadores de saída"]
        WAL["wal<br/>log próprio"]
        RFT["etcdraft<br/>único import do Raft"]
        DDB["dynamodb"]
        CSM["cosmos<br/>via Cloud-Adapter"]
        PC["peerclient<br/>gRPC"]
    end
    H --> PIN
    PG --> PIN
    AD -.-> UC
    POUT --> WAL
    POUT --> RFT
    POUT --> DDB
    POUT --> CSM
    POUT --> PC
    BOOT["bootstrap<br/>Fx liga tudo"] -.-> IN
    BOOT -.-> OUT
```

**Como ler:** as setas da esquerda (quem chama) entram pelas portas de entrada; o núcleo chama as portas de saída; os adaptadores da direita implementam essas portas. A única coisa que "conhece" tudo é o `bootstrap`.

### 4.1 Camadas e responsabilidade

| Camada | Pode importar | Não pode importar | Teste |
|---|---|---|---|
| `domain` | biblioteca padrão | tudo o mais | unitário puro, sem mock |
| `usecase` | `domain`, `port` | adaptadores, `fx`, libs de infraestrutura | unitário com fakes das portas |
| `adapter` | `port`, `domain`, sua lib de infraestrutura | outro adaptador | conformidade + integração |
| `bootstrap` | tudo | — | `fx.ValidateApp` |

### 4.2 Como o Raft e a replicação atuam (os dois logs)

O diagrama da seção 4 mostra **quem pode falar com quem**. Este mostra **o caminho do dado** dentro de um nó, e por que existem dois logs.

```mermaid
flowchart TD
    CLI["App do cliente"] --> API["dynamohttp<br/>porta de entrada"]
    API --> WS["WriteService<br/>usecase"]
    WS -->|"Propose"| RAFT["RaftNode<br/>adaptador etcdraft"]
    subgraph RL["Log do Raft: interno, vida curta"]
        RLOG[("raftlog<br/>fsync ANTES do ACK")]
    end
    RAFT -->|"grava e fsync"| RLOG
    RAFT <-->|"gRPC: AppendEntries"| PEERS["2 outros votantes AWS<br/>e 3 learners Azure"]
    PEERS -->|"índice durável de cada learner"| GATE["Gate<br/>min Raft, maioria Azure"]
    RAFT -->|"commit Raft"| GATE
    RAFT -->|"entradas commitadas<br/>Committed"| PUMP["CommitPump<br/>usecase replicate"]
    PUMP -->|"da memória, sem esperar disco"| APP["Applier"]
    PUMP -->|"em lote, FORA do caminho do ACK"| REP[("replicationlog<br/>nosso formato, vida longa")]
    GATE -->|"libera o ACK"| WS
    GATE -->|"libera aplicar até o índice"| APP
    APP -->|"escrita condicional v menor que índice"| DB[("DynamoDB ou Cosmos<br/>da célula")]
    REP -.->|"DurableIndex: limite para truncar"| RLOG
    REP -->|"ler: Azure atrasada, drctl, rebuild"| PEERS
```

**Passo a passo de uma escrita:**
1. `WriteService` chama `Propose` no `RaftNode` (única porta que fala com o Raft).
2. O Raft grava a entrada no **log do Raft** com fsync e a replica por gRPC: 2 de 3 votantes AWS decidem o commit, e os 3 learners da Azure recebem a mesma entrada.
3. Cada learner informa o índice que já tornou durável. O `Gate` guarda `min(commit do Raft, maioria Azure)`. **Só aqui o ACK é liberado.**
4. Os lotes commitados chegam ao `CommitPump`, que os entrega ao `Applier` **pela memória** e ao **log de replicação** em lote, fora do caminho do ACK.
5. O `Applier` escreve no banco da célula com escrita condicional por versão.
6. O log do Raft só é truncado até o `DurableIndex` do log de replicação (regra de segurança da 5.5.1).

**Quem grava o quê, por escrita lógica:**

| Onde | O que grava | Quando | No caminho do ACK? |
|---|---|---|---|
| `raftlog` (cada réplica) | Entrada do consenso | Antes de responder ao líder, com fsync | **Sim** |
| `replicationlog` (réplicas AWS) | Registro commitado, nosso formato | Depois do commit, em lote | Não |
| Banco da célula (DynamoDB ou Cosmos) | Item com versão | Depois do gate | Não, em Strong o ACK espera o apply serial; em Eventual não |

**Visão do cluster:** quem replica para quem, e o que cada célula guarda.

```mermaid
flowchart LR
    subgraph AWS["AWS São Paulo: 3 votantes Raft"]
        subgraph A1["Célula A1: líder"]
            A1R[("raftlog")] --- A1P["RaftNode"] --- A1L[("replicationlog")]
            A1P --> A1D[("DynamoDB A1")]
        end
        subgraph A2["Célula A2"]
            A2R[("raftlog")] --- A2P["RaftNode"] --- A2L[("replicationlog")]
            A2P --> A2D[("DynamoDB A2")]
        end
        subgraph A3["Célula A3"]
            A3R[("raftlog")] --- A3P["RaftNode"] --- A3L[("replicationlog")]
            A3P --> A3D[("DynamoDB A3")]
        end
    end
    subgraph AZ["Azure São Paulo: 3 learners, sem voto"]
        subgraph Z1["Célula Z1"]
            Z1P["RaftNode learner"] --> Z1D[("Cosmos Z1")]
        end
        subgraph Z2["Célula Z2"]
            Z2P["RaftNode learner"] --> Z2D[("Cosmos Z2")]
        end
        subgraph Z3["Célula Z3"]
            Z3P["RaftNode learner"] --> Z3D[("Cosmos Z3")]
        end
    end
    A1P <-->|"voto e commit: 2 de 3"| A2P
    A1P <-->|"voto e commit: 2 de 3"| A3P
    A1P -->|"replica e espera fsync: 2 de 3"| Z1P
    A1P --> Z2P
    A1P --> Z3P
```

Os learners da Azure guardam o log do Raft para poder confirmar durabilidade, mas só os votantes AWS decidem eleição e commit. Na Azure o log de replicação existe da mesma forma (omitido no desenho para não poluir) e alimenta o Cosmos pelo Cloud-Adapter.

## 5. Diagramas de processo

### 5.1 Escrita em modo Strong (caminho feliz)

```mermaid
sequenceDiagram
    participant App as App do cliente
    participant API as dynamohttp
    participant W as WriteService
    participant C as ConsensusEngine
    participant L as Log do Raft AWS
    participant Az as Learners Azure
    participant G as Gate
    participant AP as Applier
    participant DB as StateStore
    App->>API: PutItem
    API->>API: parse, valida, tabela lógica
    API->>W: Write(req)
    W->>C: Propose(entry)
    C->>L: replica e fsync (2 de 3 AWS)
    C->>Az: replica e fsync (2 de 3 Azure)
    L-->>G: commit Raft
    Az-->>G: maioria Azure durável
    G-->>W: gate alcançou o índice
    G-->>AP: pode aplicar até o índice (Strong: em série)
    AP->>DB: escrita condicional v menor que índice
    DB-->>AP: ok
    AP-->>W: WaitApplied liberado
    W-->>API: índice, flag degradado
    API-->>App: 200 (header x-dr-mode se degradado)
```

**O ACK sai só depois do gate e do apply na célula da líder** (6.9, item 4 do documento de arquitetura). A decisão de *quando* o apply começa é do `Applier`: no Strong, depois do gate; no Eventual, já no commit do Raft.

### 5.1b Escrita em modo Eventual (caminho feliz)

```mermaid
sequenceDiagram
    participant App as App do cliente
    participant W as WriteService
    participant C as ConsensusEngine
    participant Az as Learners Azure
    participant G as Gate
    participant AP as Applier
    participant DB as StateStore
    App->>W: PutItem (via dynamohttp)
    W->>C: Propose(entry)
    C-->>AP: commit Raft (o apply começa já)
    C->>Az: replica e fsync (2 de 3 Azure)
    AP->>DB: escrita condicional v menor que índice
    Az-->>G: maioria Azure durável
    DB-->>AP: ok
    G-->>W: gate alcançou o índice
    AP-->>W: WaitApplied liberado
    W-->>App: 200 (ACK só depois da Azure)
```

### 5.2 Ciclo de vida do processo (partida e parada)

```mermaid
flowchart TD
    A["Fx inicia"] --> B["config.Load: valida env e YAML"]
    B -->|inválida| X["processo sai com erro"]
    B --> C["abre o WAL e verifica CRC"]
    C -->|corrupção no meio| Y["recusa subir: rebuild manual"]
    C --> D["RaftNode inicia e completa o log de replicação"]
    D --> E["Applier retoma até o high-water mark anterior"]
    E --> F["readyz fica verde"]
    F --> G["porta de API abre"]
    G --> H(("em serviço"))
    H -->|SIGTERM| I["readyz vermelho, fecha a API"]
    I --> J["transfere a liderança se for a líder"]
    J --> K["drena o gate e o applier"]
    K --> L["fecha Raft e WAL"]
```

Ordem de `OnStart` no Fx: config, log, WAL, Raft, gate, applier, e **por último** a porta de API. O Fx executa `OnStop` na ordem inversa, então a API fecha primeiro e o WAL por último, sem código extra.

## 6. Diagramas de fluxo

### 6.1 Decisão de ACK (gate e modo degradado)

```mermaid
flowchart TD
    A["entry commitada no Raft"] --> B{"modo degradado?"}
    B -->|não| C["espera maioria Azure durável"]
    B -->|sim| D["gate = commit do Raft"]
    C -->|chegou| E["gate avança"]
    C -->|timeout| F["DegradeController propõe DEGRADE"]
    F --> D
    D --> E
    E --> G["libera o ACK"]
    G --> H{"Strong?"}
    H -->|sim| I["aplica no banco em série depois do gate, depois responde"]
    H -->|não| J["aplica em paralelo ao gate, ACK após a Azure e o apply local"]
    I --> K["resposta"]
    J --> K
    K --> L["header x-dr-mode se degradado"]
```

### 6.2 Leitura (Strong x Eventual)

```mermaid
flowchart TD
    A["GetItem ou Query"] --> B{"ConsistentRead true<br/>ou default strong?"}
    B -->|não| C["lê o banco local sem barreira"]
    C --> R["responde"]
    B -->|sim| D["ReadIndex na líder"]
    D -->|líder inalcançável| E["erro recuperável, SDK retenta"]
    D --> F["R = índice de leitura"]
    F --> G{"appliedIndex maior ou igual a R?"}
    G -->|sim| H["lê o banco local"]
    G -->|não| I["espera o Applier avançar"]
    I -->|deadline| E
    I --> H
    H --> R
```

### 6.3 Estados do nó

```mermaid
stateDiagram-v2
    [*] --> Recuperando
    Recuperando --> Follower: WAL ok, applier alcançou
    Recuperando --> Recusando: corrupção no meio do WAL
    Follower --> Candidato: timeout de eleição
    Candidato --> Lider: ganhou a eleição
    Candidato --> Follower: perdeu
    Lider --> ReconstruindoGate: assumiu
    ReconstruindoGate --> Lider: azureMajority reconstruído
    Lider --> Follower: transferência ou perda de quórum
    Follower --> Drenando: SIGTERM
    Lider --> Drenando: SIGTERM após transferir
    Drenando --> [*]
    Recusando --> [*]
```

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
        DM["domain<br/>Entry, Gate, Lease"]
        POUT["port out<br/>ConsensusEngine, ApplyProgress,<br/>StateStore, Forwarder"]
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

### 4.2 Como o Raft e a replicação atuam (o log do Raft)

O diagrama da seção 4 mostra **quem pode falar com quem**. Este mostra **o caminho do dado** dentro de um nó: o log do Raft é o único log do nó e a única fonte das entradas commitadas.

```mermaid
flowchart TD
    CLI["App do cliente"] --> API["dynamohttp<br/>porta de entrada"]
    API --> WS["WriteService<br/>usecase"]
    WS -->|"Propose"| RAFT["RaftNode<br/>adaptador etcdraft"]
    subgraph RL["Log do Raft: único log do nó, volume do Raft"]
        WR["Laço do Ready do shard<br/>group commit por shard, sem espera fixa"]
        RLOG[("raftlog: wal do etcd<br/>fsync ANTES do ACK")]
    end
    RAFT -->|"lote do Ready"| WR
    WR -->|"um Save, um fdatasync"| RLOG
    WR -->|"só depois do fsync: MsgAppResp, votos, auto-ACK, Advance"| RAFT
    RAFT -->|"MsgApp da líder sai antes do fsync, F3"| PEERS["2 outros votantes AWS<br/>e 3 learners Azure"]
    PEERS -->|"AppendEntries respondido só depois do fsync deles"| RAFT
    PEERS -->|"resposta do AppendEntries: termo, índice e learner, por evento"| GATE["Gate<br/>min Raft, maioria Azure"]
    RAFT -->|"commit Raft"| GATE
    RAFT -->|"índice commitado, como sinal"| PUMP["CommitPump<br/>usecase replicate"]
    PUMP -->|"entradas lidas por faixa da memória do Raft"| APP["Applier"]
    GATE -->|"libera o ACK"| WS
    GATE -->|"libera aplicar até o índice"| APP
    APP -->|"escrita condicional v menor que índice"| DB[("DynamoDB ou Cosmos<br/>da célula")]
    APP -->|"progresso (índice, ordem), CRC, sem fsync"| PROG[("progress<br/>volume do Raft")]
    APP -.->|"aplicado local: limite da compactação (RaftCompactLimit)"| RLOG
```

**Passo a passo de uma escrita:**
1. `WriteService` chama `Propose` no `RaftNode` (única porta que fala com o Raft).
2. O Raft envia a entrada aos pares (só o `MsgApp` da líder sai antes do fsync, F3) e o laço do `Ready` grava o lote no **log do Raft** com um fsync; nenhuma resposta (`MsgAppResp`, voto, auto-ACK da líder) sai antes do fsync ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)). Na replicação por gRPC, 2 de 3 votantes AWS decidem o commit, e os 3 learners da Azure recebem a mesma entrada.
3. Cada learner informa o índice que já tornou durável **na resposta do `AppendEntries`**, com o termo e a identidade dele ([ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)). Cada resposta alimenta o `Gate` na hora, sem esperar um commit novo; o gate só adota o termo atual e guarda `min(commit do Raft, maioria Azure)`. **Só aqui o ACK é liberado.**
4. O índice commitado chega ao `CommitPump` como sinal; ele lê as entradas commitadas por faixa **da memória do Raft** e as entrega ao `Applier`, fora do caminho do ACK. O progresso dos learners e o gate correm em goroutines próprias do `CommitPump` ([ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)): um applier lento não atrasa o gate.
5. O `Applier` escreve no banco da célula com escrita condicional por versão, **sempre depois do gate** ([ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md)) e **em paralelo por chave**, com a mesma chave na ordem do log ([ADR-058](../03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)); o `applied` é a marca d'água contígua, e o progresso salvo é ela `(índice, ordem)` ([ADR-048](../03_adr/ADR-048-skip-por-item.md)).
6. O log do Raft só é compactado até `domain.RaftCompactLimit`, o menor entre o aplicado local, o índice durável da maioria Azure enquanto a Azure estiver dentro da janela e F − 1 de uma reconstrução em tabela sombra em curso, com uma folga até o menor `match` dos votantes AWS limitada por `RAFT_RETAIN_MAX` ([ADR-063](../03_adr/ADR-063-snapshot-e-compactacao-do-log-do-raft.md), pendente). O que ele retém serve a Azure atrasada pelo próprio Raft (`MsgApp`); acima do teto de 70% do volume do Raft ([ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)), a Azure sai da janela e volta por reconstrução em tabela sombra ([ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).

**Quem grava o quê, por escrita lógica:**

| Onde | O que grava | Quando | No caminho do ACK? |
|---|---|---|---|
| `raftlog` (cada réplica) | Entrada do consenso | Antes de responder ao líder, com fsync | **Sim** |
| Banco da célula (DynamoDB ou Cosmos) | Item com versão | Depois do gate, em paralelo por chave (ADR-058) | **Sim**, na célula da líder o ACK espera o apply (o `t_apply` está no orçamento, [ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)) |
| Volume do Raft, arquivo `progress` (progresso do applier) | Progresso por item `(índice, ordem)`, com CRC | Depois de cada item aplicado, sem fsync | Não |

**Visão do cluster:** quem replica para quem, e o que cada célula guarda.

```mermaid
flowchart LR
    subgraph AWS["AWS São Paulo: 3 votantes Raft"]
        subgraph A1["Célula A1: líder"]
            A1R[("raftlog")] --- A1P["RaftNode"]
            A1P --> A1D[("DynamoDB A1")]
        end
        subgraph A2["Célula A2"]
            A2R[("raftlog")] --- A2P["RaftNode"]
            A2P --> A2D[("DynamoDB A2")]
        end
        subgraph A3["Célula A3"]
            A3R[("raftlog")] --- A3P["RaftNode"]
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

Os learners da Azure guardam o log do Raft para poder confirmar durabilidade, mas só os votantes AWS decidem eleição e commit. Na Azure o applier de cada célula lê as entradas commitadas do próprio log do Raft (omitido no desenho para não poluir) e alimenta o Cosmos pelo Cloud-Adapter.

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
    G-->>AP: pode aplicar até o índice (paralelo por chave)
    AP->>DB: escrita condicional v menor que índice
    DB-->>AP: ok
    AP-->>W: WaitApplied liberado
    W-->>API: índice, flag degradado
    API-->>App: 200 (header x-dr-mode se degradado)
```

**O ACK sai só depois do gate e do apply na célula da líder** (6.9, item 4 do documento de arquitetura). O apply é sempre depois do gate, em todas as instalações (modo único, [ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md)), em paralelo por chave, e o ACK espera a marca d'água contígua ([ADR-058](../03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)). Antes de responder, a líder ainda confere a validade do lease se o shard estiver degradado ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)).

### 5.1b Escrita em modo degradado (política `accept`, com lease)

Há um único modo, strong ([ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md)): a escrita tem um único caminho, o do 5.1. O que muda o caminho é a Azure fora do alcance, e só com `DEGRADED_POLICY=accept` ([ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)).

```mermaid
sequenceDiagram
    participant App as App do cliente
    participant W as WriteService
    participant D as DegradeService
    participant C as ConsensusEngine
    participant G as Gate
    participant AP as Applier
    App->>W: PutItem
    W->>C: Propose(entry)
    C-->>G: commit Raft
    Note over G: a Azure não confirma em DEGRADE_AFTER (2 s)
    W->>D: Enter
    D->>C: Propose(DEGRADE, epoch, D)
    C-->>D: commit (o DEGRADE só marca o modo)
    D->>G: SetDegraded(true) depois do modo marcado
    G-->>AP: pode aplicar até o commit Raft
    AP-->>W: WaitApplied liberado
    W->>D: CheckLease (a cada ACK): now menor que t_c + D?
    alt lease válido
        W-->>App: 200 com x-dr-mode: degraded
    else lease vencido (t_c + D esgotado ou sem t_c)
        W-->>App: 503 LEASE_EXPIRED (leitura e Raft seguem)
    end
```

O prazo D conta desde `t_c`, o último contato com a maioria dos learners da Azure, e não desde a proposta do `DEGRADE`: se o `DEGRADE` só é proposto muito depois de `t_c`, o lease já nasce vencido ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)). Com `reject` o `Enter` recusa sempre: o gate fica sem a Azure e o cliente recebe `503 PENDING` no fim do prazo, com RPO 0.

### 5.2 Ciclo de vida do processo (partida e parada)

```mermaid
flowchart TD
    A["Fx inicia"] --> B["config.Load: valida env e YAML<br/>DEGRADED_POLICY obrigatória, DEGRADED_LEASE com accept"]
    B -->|inválida| X["processo sai com erro"]
    B --> C["abre o WAL e verifica CRC"]
    C -->|corrupção no meio| Y["recusa subir: rebuild manual"]
    C --> D["RaftNode inicia em Applied = retomada − 1<br/>retomada = progresso do applier, ou 1"]
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

### 6.1 Decisão de ACK (gate, política e lease)

```mermaid
flowchart TD
    A["entry commitada no Raft"] --> B{"shard degradado?"}
    B -->|não| C["espera maioria Azure durável<br/>termo atual, por evento"]
    B -->|sim| D["gate = commit do Raft"]
    C -->|chegou| E["gate avança"]
    C -->|"DEGRADE_AFTER sem a Azure"| P{"DEGRADED_POLICY"}
    P -->|accept| F["propõe DEGRADE, que marca o modo<br/>lease D desde t_c"]
    P -->|reject| R["segue esperando<br/>até o prazo: 503 PENDING"]
    F --> D
    D --> E
    E --> G["apply no banco, depois do gate, paralelo por chave"]
    G --> L{"degradado e<br/>now maior ou igual a t_c + D?"}
    L -->|sim| X["503 LEASE_EXPIRED"]
    L -->|não| K["resposta"]
    K --> M["header x-dr-mode se degradado"]
```

### 6.2 Leitura (sempre forte, ADR-057)

```mermaid
flowchart TD
    A["GetItem, Query ou Scan"] --> I0{"IndexName de GSI?"}
    I0 -->|sim| V["400 ValidationException (ADR-050)"]
    I0 -->|não| D["ReadIndex e gate<br/>na mesma resposta da líder"]
    D -->|líder inalcançável ou gate não reconstruído| E["503 recuperável, SDK retenta"]
    D --> F["R = min(ReadIndex, gate)"]
    F --> G{"appliedIndex maior ou igual a R?"}
    G -->|sim| H["lê o banco local"]
    G -->|não| J{"applier parado<br/>por entrada venenosa?"}
    J -->|sim| E
    J -->|não| I["espera o Applier avançar"]
    I -->|deadline| E
    I --> H
    H --> R["responde"]
```

A barreira é `min(ReadIndex, gate)` ([ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)): toda escrita ACKada está abaixo do gate, então uma Azure lenta não atrasa a leitura forte. Isso exige que `applied` nunca passe do gate.

### 6.3 Estados do nó

```mermaid
stateDiagram-v2
    [*] --> Recuperando
    Recuperando --> Follower: WAL ok, applier alcançou
    Recuperando --> Recusando: corrupção no meio do WAL
    Follower --> Candidato: timeout de eleição
    Candidato --> Lider: ganhou a eleição
    Candidato --> Follower: perdeu
    Lider --> ReconstruindoGate: assumiu (termo novo)
    ReconstruindoGate --> Lider: 2 de 3 learners confirmaram no termo novo
    Lider --> Follower: transferência ou perda de quórum
    Follower --> Drenando: SIGTERM
    Lider --> Drenando: SIGTERM após transferir
    Drenando --> [*]
    Recusando --> [*]
```

### 6.4 Estados do modo degradado (lease)

```mermaid
stateDiagram-v2
    [*] --> Normal
    Normal --> Degradado: DEGRADE marca o modo (só política accept)
    Degradado --> Normal: RESTORE, a Azure alcançou o log
    Degradado --> Degradado: RENEW humano ou novo contato com a maioria Azure
    Degradado --> LeaseExpirado: now maior ou igual a t_c + D, conferido a cada ACK
    Degradado --> LeaseExpirado: líder nova ou reinício herda o DEGRADE sem t_c
    LeaseExpirado --> Degradado: contato com a maioria Azure ou RENEW humano
    LeaseExpirado --> Normal: RESTORE
    note right of LeaseExpirado
        escrita recusa com 503 LEASE_EXPIRED
        leitura e Raft seguem
    end note
```

O estado `Normal` com política `reject` nunca sai para `Degradado`. O lease conta desde `t_c`, o último contato da líder com a maioria Azure. A Azure só é promovida (ação humana) depois de D mais a margem M (deriva máxima de relógio mais pausa máxima de processo) contados desde o último contato com a AWS; a regra de prorrogação contra promoção (H-DR13) está para o modelo TLA+.

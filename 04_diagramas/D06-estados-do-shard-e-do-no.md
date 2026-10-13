# D06 — Estados do shard e do nó

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

Dois níveis: os **modos de operação do shard** (decididos pelo consenso e pela política da instalação) e os **estados do processo** em cada nó. Com `DEGRADED_POLICY=accept` o modo degradado tem **lease D**; ao fim dele a AWS recusa escrita (`LeaseExpirado`) e só uma pessoa prorroga. Com `reject` não existe `Degradado`: sem o gate da Azure as escritas recebem `503 PENDING`. A Azure só é promovida depois de **D + M**.

## Modos de operação do shard

```mermaid
stateDiagram-v2
    [*] --> Normal
    Normal --> Degradado: accept, Azure ou link inalcançável por DEGRADE_AFTER, DEGRADE com lease D
    Normal --> SemGateAzure: reject, Azure ou link inalcançável, 503 PENDING
    SemGateAzure --> Normal: gate da Azure volta
    Degradado --> LeaseExpirado: fim de D desde o último contato, ou líder nova, 503 LEASE_EXPIRED
    LeaseExpirado --> Degradado: RENEW por pessoa ou contato com a Azure
    Degradado --> Ressincronizando: Azure volta
    LeaseExpirado --> Ressincronizando: Azure volta
    Ressincronizando --> Normal: lag baixo e estável, RESTORE
    Ressincronizando --> Degradado: Azure cai de novo
    Normal --> PromovidoAzure: AWS perdida, promoção humana
    SemGateAzure --> PromovidoAzure: AWS perdida, promoção humana
    Degradado --> PromovidoAzure: AWS perdida, só depois de D mais M
    LeaseExpirado --> PromovidoAzure: promoção humana, só depois de D mais M
    PromovidoAzure --> Normal: AWS volta e failback planejado
    Degradado --> Parado: AWS cai sem decisão humana
    Parado --> PromovidoAzure: promoção humana, só depois de D mais M
```

## Estados do nó

```mermaid
stateDiagram-v2
    [*] --> Recuperando
    Recuperando --> Follower: log ok, applier alcançou
    Recuperando --> Recusando: corrupção no meio do log
    Follower --> Candidato: timeout de eleição
    Candidato --> Lider: ganhou a eleição
    Candidato --> Follower: perdeu
    Lider --> ReconstruindoGate: assumiu, sem ACK nem leitura forte
    ReconstruindoGate --> Lider: gate reconstruído só com progresso confirmado no termo novo
    Lider --> Follower: transferência ou perda de quórum
    Follower --> Drenando: SIGTERM
    Lider --> Drenando: SIGTERM após transferir
    Drenando --> [*]
    Recusando --> [*]
```

## Onde é detalhado

- Cenários: [C17](../02_cenarios/atualizados/C17-maquina-de-estados.md) e [C18](../02_cenarios/atualizados/C18-partida-e-recuperacao-do-no.md).
- Modo degradado em detalhe: [X02](../02_cenarios/complexos/X02-modo-degradado.md). Lease, `RENEW` e promoção depois de D + M: [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) e [ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md). Política por instalação: [ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md).

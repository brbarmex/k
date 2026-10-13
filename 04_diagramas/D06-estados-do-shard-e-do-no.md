# D06 — Estados do shard e do nó

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

Dois níveis: os **modos de operação do shard** (decididos pelo consenso) e os **estados do processo** em cada nó.

## Modos de operação do shard

```mermaid
stateDiagram-v2
    [*] --> Normal
    Normal --> Degradado: Azure ou link inalcançável por DEGRADE_AFTER
    Degradado --> Ressincronizando: Azure volta
    Ressincronizando --> Normal: lag baixo e estável, RESTORE
    Ressincronizando --> Degradado: Azure cai de novo
    Normal --> PromovidoAzure: AWS perdida, promoção humana
    Degradado --> PromovidoAzure: AWS perdida, perda declarada
    PromovidoAzure --> Normal: AWS volta e failback planejado
    Degradado --> Parado: AWS cai sem decisão humana
    Parado --> PromovidoAzure: promoção humana
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
    Lider --> ReconstruindoGate: assumiu
    ReconstruindoGate --> Lider: gate reconstruído
    Lider --> Follower: transferência ou perda de quórum
    Follower --> Drenando: SIGTERM
    Lider --> Drenando: SIGTERM após transferir
    Drenando --> [*]
    Recusando --> [*]
```

## Onde é detalhado

- Cenários: [C17](../02_cenarios/atualizados/C17-maquina-de-estados.md) e [C18](../02_cenarios/atualizados/C18-partida-e-recuperacao-do-no.md).
- Modo degradado em detalhe: [X02](../02_cenarios/complexos/X02-modo-degradado.md).

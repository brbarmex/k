# D01 — Visão do sistema numa página

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

O problema e a solução em um desenho: a aplicação escreve como se falasse com o DynamoDB; a camada só confirma depois que o dado está durável na AWS **e** na Azure.

## Contexto

```mermaid
flowchart LR
    APP["Aplicações do cliente<br/>AWS SDK v2 ou superior"] --> DR["DataReplication<br/>camada intermediária"]
    DR --> AWSDB[("DynamoDB<br/>AWS primária")]
    DR ==>|"link dedicado"| AZ["DataReplication na Azure<br/>learners do consenso"]
    AZ --> AZDB[("Cosmos DB<br/>Azure failover")]
    ROUTER["Compass-Router e Agent<br/>externos ao escopo"] -.-> APP
```

## Regra do ACK

```mermaid
flowchart TD
    W["Escrita do cliente"] --> R["Commit Raft: 2 de 3 votantes AWS com fsync"]
    R --> G{"Gate"}
    W --> Z["Replicação aos learners Azure: 2 de 3 com fsync"]
    Z --> G
    G --> AP["Apply no banco da célula da líder"]
    AP --> A["ACK ao cliente"]
    G -.->|"Azure inalcançável por 2 s: modo degradado, RPO maior que 0 e header x-dr-mode"| A
```

## Onde é detalhado

- Problema, solução e restrições: [documento principal](../DataReplication_Versao_1.md).
- Estrutura detalhada: [C01](../02_cenarios/atualizados/C01-visao-geral.md) e [C02](../02_cenarios/atualizados/C02-dentro-da-celula.md).
- Gate: [D05](D05-gate-e-quorum.md) e [X01](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md).

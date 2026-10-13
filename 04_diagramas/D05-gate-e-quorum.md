# D05 — Quórum e gate

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

Só os 3 votantes AWS elegem e decidem o commit. Os 3 learners da Azure recebem as mesmas entradas e informam o índice **durável**. O gate é `min(commit do Raft, 2º maior índice durável dos learners)` e **nunca recua**. No modo degradado, a Azure sai da conta.

## Composição do gate

```mermaid
flowchart LR
    subgraph VOT["Votantes AWS"]
        A1["A1 líder"]
        A2["A2"]
        A3["A3"]
    end
    subgraph LRN["Learners Azure, sem voto"]
        Z1["Z1"]
        Z2["Z2"]
        Z3["Z3"]
    end
    A1 -->|"2 de 3 com fsync"| RC["commit do Raft"]
    A2 --> RC
    A3 --> RC
    Z1 -->|"índice durável"| AQ["2º maior índice durável"]
    Z2 --> AQ
    Z3 --> AQ
    RC --> MIN["gate = min dos dois"]
    AQ --> MIN
    MIN --> MONO["monotônico: current = max de current e o novo valor"]
    DEG["Modo degradado"] -.->|"ignora o AQ"| MIN
```

## Onde é detalhado

- Definição, regras e troca de líder: [X01](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md) (inclui as propriedades TLA+).
- Decisões: [ADR-017](../03_adr/ADR-017-azure-learners-e-gate.md) e [ADR-029](../03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md).
- Implementação do gate: [05_implementacao/03](../05_implementacao/03_exemplos_de_codigo.md) (7.2).

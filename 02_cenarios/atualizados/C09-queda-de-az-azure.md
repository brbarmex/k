# C09 — Queda de uma AZ na Azure

**Status:** ✅ Atualizado (modelo v1) · **ADRs:** [ADR-017](../../03_adr/ADR-017-azure-learners-e-gate.md)

## Cenário

Uma AZ da Azure cai (por exemplo, Z3). Z1 e Z2 seguem como maioria dos learners (2 de 3), então o gate segue satisfeito. Nada muda para o cliente. Quando Z3 volta, faz catch-up pelo log antes de contar de novo para o gate. Se **duas** AZs da Azure caem, é como a Azure inalcançável: entra o modo degradado ([C12](C12-modo-degradado.md)).

## Diagrama

```mermaid
sequenceDiagram
    autonumber
    participant L as Líder AWS
    participant AW as Votantes AWS
    participant Z1 as Z1
    participant Z2 as Z2
    participant Z3 as Z3
    Z3-xZ3: AZ 3 cai
    L->>AW: AppendEntries N
    L->>Z1: AppendEntries N
    L->>Z2: AppendEntries N
    L-->>Z3: AppendEntries N sem resposta
    AW-->>L: ok 2 de 3
    Z1-->>L: fsync ok
    Z2-->>L: fsync ok
    Note over L: commit Raft e maioria Azure com Z1 e Z2, gate libera
    Z3->>L: volta e pede a partir do último índice
    L-->>Z3: catch-up pelo log de replicação
```

## Etapas

1. A AZ 3 da Azure cai; Z3 fica fora.
2. A líder replica N para os votantes AWS e para Z1, Z2 e Z3 (sem resposta de Z3).
3. 2 de 3 votantes AWS e 2 de 3 learners (Z1 e Z2) confirmam o fsync; o gate libera.
4. Z3 volta e recebe as entradas que faltam pelo log de replicação.

## Falhas e tratativas

- **Duas AZs da Azure caem.** Do ponto de vista da AWS é igual à Azure inalcançável: modo degradado (C12) e ressincronização depois (C13).
- **Z3 volta com disco novo.** Identidade nova como learner e reconstrução ([C21](C21-disco-fsync-e-reconstrucao.md)).

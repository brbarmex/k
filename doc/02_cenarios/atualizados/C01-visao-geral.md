# C01 — Visão geral da arquitetura

**Status:** ✅ Atualizado (modelo v1) · **ADRs:** [ADR-016](../../03_adr/ADR-016-app-unico-por-celula-monolito-modular.md), [ADR-017](../../03_adr/ADR-017-azure-learners-e-gate.md), [ADR-003](../../03_adr/ADR-003-aws-primaria-azure-failover.md), [ADR-054](../../03_adr/ADR-054-banco-por-celula-mantido.md)

## Cenário

Estrutura de um shard em regime normal. Cada célula é um cluster Kubernetes (EKS ou AKS) numa AZ. No namespace do cliente rodam as apps e **um único** `DataReplication`. As 3 células AWS são votantes do Raft; as 3 células Azure são learners. O Compass-Router e o Agent do control plane são externos ao time.

## Diagrama

```mermaid
flowchart LR
    Cliente(["Cliente"]) --> CR["Compass-Router<br/>externo"]
    Agent["Agent do control plane<br/>AWS e Azure"] -.-> CR
    subgraph AWS["AWS sa-east-1: votantes Raft"]
        subgraph A1["Célula A1 - EKS - AZ a"]
            APP1["Apps do cliente"] --> DR1["DataReplication<br/>líder"]
            DR1 --- LG1[("log do Raft<br/>log de replicação")]
            DR1 --> DB1[("DynamoDB A1")]
        end
        subgraph A2["Célula A2 - EKS - AZ b"]
            APP2["Apps do cliente"] --> DR2["DataReplication"]
            DR2 --- LG2[("logs")]
            DR2 --> DB2[("DynamoDB A2")]
        end
        subgraph A3["Célula A3 - EKS - AZ c"]
            APP3["Apps do cliente"] --> DR3["DataReplication"]
            DR3 --- LG3[("logs")]
            DR3 --> DB3[("DynamoDB A3")]
        end
    end
    subgraph AZR["Azure Brazil South: learners"]
        subgraph Z1["Célula Z1 - AKS - AZ 1"]
            ZA1["Apps do cliente"] --> ZD1["DataReplication<br/>learner"]
            ZD1 --> ZB1[("Cosmos Z1<br/>via Cloud-Adapter")]
        end
        subgraph Z2["Célula Z2 - AKS - AZ 2"]
            ZA2["Apps do cliente"] --> ZD2["DataReplication<br/>learner"]
            ZD2 --> ZB2[("Cosmos Z2")]
        end
        subgraph Z3["Célula Z3 - AKS - AZ 3"]
            ZA3["Apps do cliente"] --> ZD3["DataReplication<br/>learner"]
            ZD3 --> ZB3[("Cosmos Z3")]
        end
    end
    CR ==> APP1
    CR -.-> ZA1
    DR1 <==>|"voto e commit: 2 de 3"| DR2
    DR1 <==>|"voto e commit: 2 de 3"| DR3
    DR1 ==>|"link dedicado: replica e espera fsync"| ZD1
    DR1 ==> ZD2
    DR1 ==> ZD3
```

## Etapas

1. O cliente chama o Compass-Router, que entrega a requisição às apps de uma célula AWS. A rota para a Azure só é usada quando o Agent manda.
2. As apps falam com o `DataReplication` do próprio namespace por uma única porta (API compatível com DynamoDB).
3. Se a célula não for a líder, o `DataReplication` encaminha a escrita à líder por gRPC (1 salto, ver C06).
4. A líder grava no log do Raft com fsync e replica para os 2 votantes AWS e para os 3 learners Azure.
5. O gate libera o ACK quando há commit Raft (2 de 3 AWS) e maioria Azure durável (2 de 3).
6. Cada célula tem o seu banco e o seu applier. A escrita lógica é aplicada nos 6 bancos (3 DynamoDB, 3 Cosmos). O DynamoDB é regional e o Cosmos tem redundância de zona, então os seis bancos **não são seis domínios físicos de falha**: o que eles compram é isolamento lógico (corrupção ou throttling de uma tabela não atinge as outras), e a durabilidade por AZ vem dos três logs Raft ([ADR-054](../../03_adr/ADR-054-banco-por-celula-mantido.md)).

## Falhas e tratativas

- **Compass-Router ou Agent indisponível.** Dependência externa, fora do escopo do time. O router sobrevive à perda total da AWS e o Agent roda nas duas nuvens (confirmado pelo Bruno).
- **Certificados mTLS ou credenciais entre nuvens expiram.** Para a AWS é igual à Azure cair: conforme a `DEGRADED_POLICY`, entra o modo degradado com lease (`accept`) ou a escrita responde `503 PENDING` (`reject`) (C12). Alerta de expiração com antecedência.

## Pontos em aberto

- Link lento ou saturado (falha cinzenta): [C25](C25-link-lento-faixa-cinzenta.md).

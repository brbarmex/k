# D02 — Implantação de uma célula no Kubernetes

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

Cada célula é um cluster (EKS ou AKS) em uma AZ. No namespace do cliente rodam as aplicações e **um** pod `DataReplication`, sem sidecar e sem HPA. O volume do log é dedicado, e a imposição "só o `DataReplication` escreve no banco" é feita por IAM.

## Dentro de uma célula

```mermaid
flowchart TD
    subgraph NS["Namespace do cliente, NetworkPolicy fechada"]
        A1["App 1"] --> SVC["Service interno<br/>porta de API"]
        A2["App 2 a 10"] --> SVC
        SVC --> POD["Pod DataReplication<br/>QoS Guaranteed, PDB maxUnavailable 1"]
    end
    POD --- VOL[("Volume de bloco dedicado<br/>log do Raft e log de replicação")]
    POD -->|"identidade da carga de trabalho"| DB[("Banco da célula")]
    POD <-->|"mTLS, porta de replicação por balanceador interno"| PEERS["DataReplication das outras células"]
    POD --> ADM["Porta de administração<br/>livez, readyz, metrics"]
    A1 -. "IAM negativo: sem escrita direta" .-> DB
```

## Onde é detalhado

- Modelo e riscos: [01_forma_do_app_e_escopo](../01_arquitetura/01_forma_do_app_e_escopo.md) (seção 3).
- Probes, PDB e desligamento limpo: [08_resiliencia_autocura_e_timeouts](../01_arquitetura/08_resiliencia_autocura_e_timeouts.md) (9.2).
- Rede, identidade e imagem: [09_12factors_cloud_native_e_sdks](../01_arquitetura/09_12factors_cloud_native_e_sdks.md) (seção 11) e [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md).

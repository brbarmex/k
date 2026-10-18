# FP-08 · Nó EC2 perdido e volume EBS reanexado em outro nó

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento: no EKS, a falha comum não é o pod, é o nó |
| Requisitos | FT, HA (MTTR da célula), RPO = 0 |
| Suporte na POC hoje | **Sim no app; depende da infraestrutura.** O app volta igual a FP-01, mas o tempo é dominado pelo Kubernetes e pelo EBS. |
| Injeção | **a** `aws:ec2:terminate-instances` no nó da líder e, noutra execução, da follower; **b** `aws:ec2:stop-instances` (nó some sem desligamento gracioso do kubelet) |
| Duração | 10 min antes, falha, até a célula voltar + 10 min |

## Análise crítica

- Ponto oculto importante: quando o nó morre sem desligamento gracioso, o volume EBS continua "anexado" ao nó morto. O controlador de attach/detach do Kubernetes espera um tempo de segurança (historicamente ~6 min) antes de forçar a desanexação, e o pod novo fica em `ContainerCreating` com erro de multi-attach. O MTTR da célula vira minutos. Mitigações (taint `node.kubernetes.io/out-of-service`, desligamento gracioso do nó) precisam ser configuradas e o cenário prova se estão.
- Para a líder, o Delta-T de escrita é o de FP-03 (o resto continua); o que muda é o tempo em que o sistema roda com 2 células.
- A volta da célula usa o **mesmo** volume (mesmo WAL e mesmo NODE_ID): correto, porque o disco não falhou.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | MTTR decomposto | nó perdido → pod agendado → volume desanexado → anexado → container iniciado → pronta → alcançou (eventos do Kubernetes + RTO-07) |
| A3 | Escrita pelas outras células | como FP-01 A3 (follower) ou FP-03 A3 (líder) |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por fase; L-APPLY da célula na volta.
- Golden signals: G-TRF-1..2, G-ERR-1..5, G-SAT-2.
- Além: B-K8S (eventos `FailedAttachVolume`, `Multi-Attach error`, tempo em `ContainerCreating`), CloudWatch do EBS (estado do volume), RTO-07.

## Lacunas

M-06.

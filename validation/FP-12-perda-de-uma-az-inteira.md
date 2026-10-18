# FP-12 · Perda de uma AZ inteira

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento: é o domínio de falha para o qual a célula foi desenhada |
| Requisitos | FT, HA, RPO = 0 |
| Suporte na POC hoje | **Sim.** As outras duas AZs formam maioria; o DynamoDB é regional e a tabela da célula perdida continua existindo. |
| Injeção | cenário do FIS "AZ Availability: Power Interruption" (ou `aws:network:disrupt-connectivity` nas subnets da AZ + parada dos nós), na AZ da líder e, noutra execução, de uma follower |
| Duração | 10 min antes, 30 min de AZ fora, volta + 10 min |

## Análise crítica

- Diferenças para FP-03/FP-06: o plano de controle do EKS daquele cluster (se for um cluster por AZ, ele é regional e sobrevive, mas os nós não), o gerador de carga daquela AZ (precisa estar fora dela, AMB-09) e o Prometheus daquela AZ (AMB-10). Sem cuidar disso, o próprio teste perde a visão do que acontece.
- O volume EBS da AZ perdida fica inacessível até a AZ voltar; não há como mover a célula para outra AZ sem recriar o volume (e isso é FD-05, identidade nova).
- O NLB/DNS dos pares (AMB-06) que aponta para a AZ perdida pode responder com timeout em vez de recusa: o gRPC do Raft fica com mensagens presas até o timeout. Medir `dr_peer_send_dropped_total`.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Delta-T (AZ da líder) | RTO-02..05 |
| A3 | Sem impacto (AZ de follower) | como FP-01 A2, A3 |
| A4 | Volta da AZ | RTO-07 da célula; B-DRENO |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK, L-FWD, L-LOOP (envio a um par inalcançável não pode travar o laço).
- Golden signals: G-TRF-1..3, G-ERR-1..5, G-SAT-1.
- Além: B-RAFT-1..5, `dr_peer_send_dropped_total`, B-REDE, B-K8S, RTO-01..08.

## Lacunas

M-10.

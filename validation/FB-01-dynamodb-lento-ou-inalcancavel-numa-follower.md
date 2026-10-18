# FB-01 · DynamoDB lento ou inalcançável numa célula follower

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do banco (complemento) |
| Origem | Complemento (C35 do pacote: banco lento não derruba a liderança; equivalente local `task test-slow-db`) |
| Requisitos | HA (a liderança não cai), RPO = 0 (o log guarda o que o banco não recebeu) |
| Suporte na POC hoje | **Sim.** O laço do Raft não espera o apply (ADR-060); o applier repete erro transitório sem limite. |
| Injeção | **a** inalcançável: `aws:network:disrupt-connectivity` com `scope: dynamodb` nas subnets da célula; **b** lento/limitado: tabela da célula em modo provisionado com WCU baixo (gera `ProvisionedThroughputExceededException`, tratado como transitório) |
| Duração | 10 min antes, 2 min e 10 min de falha, até alcançar + 10 min |

## Análise crítica

- Cuidado na escolha da injeção: negar por política de IAM ou do VPC endpoint devolve `AccessDeniedException`, que a POC classifica como **permanente** e estaciona o applier como se fosse item venenoso. Isso é outro cenário (FB-03). Aqui a falha precisa ser de rede ou de capacidade.
- Escritas continuam confirmadas pela líder e pela outra follower. A follower afetada acumula atraso de apply sem nenhuma recusa (a admissão só olha a líder).
- Leituras fortes nessa follower ficam presas até `read_timeout`.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Termo não muda | `dr_raft_term` constante nas 3 (é o critério central) |
| A2 | RPO = 0 | ORC-04 após a volta do banco |
| A3 | Laço saudável na follower afetada | L-TICK p99 ≤ 0,15 s durante a falha |
| A4 | Alcance após a volta | B-DRENO > 0; tempo até `dr_apply_lag == 0` |

## Métricas

- Latência (p90, p99, p99.9, max): L-APPLY da afetada (com repetições), L-READ nela, L-TICK, L-LOOP, L-ACK.
- Golden signals: G-TRF-2, G-ERR-4, G-SAT-2, G-SAT-3 (`dr_commit_undelivered` cresce), G-SAT-5 (memória da afetada), G-SAT-7 (`ThrottledRequests` na variante b).
- Além: B-DRENO, B-APP, M-07, M-11; CloudWatch do DynamoDB da tabela.

## Lacunas

M-07, M-11.

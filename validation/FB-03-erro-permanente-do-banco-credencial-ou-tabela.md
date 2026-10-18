# FB-03 · Erro permanente do banco: credencial negada ou tabela inexistente

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do banco (complemento) |
| Origem | Complemento |
| Requisitos | HA, RPO = 0 |
| Suporte na POC hoje | **Parcial e com classificação questionável.** Qualquer código de erro do DynamoDB fora da lista de transitórios (`classify` em `adapter/out/dynamodb/store.go`) é tratado como permanente: `AccessDeniedException`, `ExpiredTokenException`, `ResourceNotFoundException`, `UnrecognizedClientException`. O applier **estaciona** como se fosse item venenoso (ADR-038), `/readyz` passa a falhar ("applier parado") e a volta exige ação humana. |
| Injeção | **a** negar `dynamodb:PutItem` na política do papel IRSA da célula por 2 min; **b** política do VPC endpoint negando; **c** renomear/apagar a tabela da célula (só em ambiente isolado); numa follower e na líder |
| Duração | até a volta, com ação humana registrada |

## Análise crítica

- Credencial expirada, política alterada por engano e endpoint mal configurado são falhas de **ambiente**, não do item. Tratá-las como item venenoso faz uma mudança de IAM de 2 min parar uma célula até alguém intervir. Na líder, para a escrita do sistema inteiro.
- O cenário mede o que acontece depois de desfazer a negação: o applier continua estacionado? Reiniciar o pod resolve (o replay recomeça do progresso)? Qual é o procedimento?

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 após a recuperação |
| A2 | Detecção | `dr_applier_stopped == 1` e `dr_ready == 0` em segundos |
| A3 | Procedimento de volta | passos e tempo até `dr_applier_stopped == 0` |
| A4 | Na líder: o que o cliente vê | ledger: PENDING, depois 503/erro do balanceador (célula fora do Service) |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK, L-READ (falha rápida com `applier_stopped`).
- Golden signals: G-ERR-1, G-ERR-4 (`applier_stopped`), G-TRF-2, G-SAT-2.
- Além: B-APP, B-K8S (`kube_pod_status_ready`), M-07 (sem ele, o código do erro só aparece no log).

## Lacunas

M-07; decisão de reclassificar erros de credencial como transitórios com alerta.

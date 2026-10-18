# FC-03 · Tempestade de novas tentativas durante uma troca de líder

| Campo | Valor |
|---|---|
| Categoria | Carga (complemento) |
| Origem | Complemento |
| Requisitos | HA (o sistema volta sob a carga acumulada), RPO = 0 |
| Suporte na POC hoje | **Parcial.** Há teto de requisições esperando líder (`MaxWaitingLeader`) e contrapressão; não há dedupe de retry (CE-04). |
| Injeção | FP-03 variante b com clientes de **SDK real** (Java 2.x ou Go v2, timeout 30 s, 3 tentativas, backoff padrão) em vez de k6 sem retry, a CRG-2 |
| Duração | 10 min antes, falha, 10 min depois |

## Análise crítica

- O k6 sem retry mede o sistema; o SDK real mede o que o cliente vive. Durante a eleição, as tentativas se acumulam e chegam juntas na nova líder, que já pode estar atrasada no apply (CE-05). O risco é a nova líder entrar direto em contrapressão e a recuperação demorar mais que a eleição.
- Sem dedupe, cada tentativa que chega depois de uma PENDING já confirmada vira entrada duplicada.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 por operação lógica | ORC-04 agrupado por operação lógica |
| A2 | Amplificação medida | requisições recebidas ÷ operações lógicas na janela |
| A3 | Volta sob carga | RTO-06 com SDK ≤ 2 × RTO-06 com k6 (hipótese) |

## Métricas

- Latência (p90, p99, p99.9, max): latência por operação lógica (todas as tentativas), L-ACK por tentativa.
- Golden signals: G-TRF-1 (pico), G-ERR-3, G-SAT-1.
- Além: B-CUSTO, B-IDX (entradas no log × operações lógicas), RTO-04..06.

## Lacunas

Dedupe na POC; M-09.

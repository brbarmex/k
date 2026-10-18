# CF-03 · Tráfego distribuído nas três células ao mesmo tempo

| Campo | Valor |
|---|---|
| Categoria | Caminho feliz (complemento) |
| Origem | Complemento: CF-01 e CF-02 isolam o caminho; em produção o tráfego chega às três |
| Requisitos | RPO = 0, HA |
| Suporte na POC hoje | **Sim** |
| Injeção | nenhuma |
| Duração | 2 + 10 min, CRG-1 e CRG-2 divididos em partes iguais entre as 3 APIs |

## Objetivo

Medir o comportamento com a mistura real (1/3 direto na líder, 2/3 encaminhado) e servir de **carga de fundo padrão** para todos os cenários de falha: sem tráfego nas três células, uma falha da líder não exercita o encaminhamento durante a eleição.

## Análise crítica

- É o perfil que mais se parece com o produto, onde o roteador externo não sabe quem é a líder. Os cenários de falha da lista do Bruno não dizem por onde chega o tráfego: aqui fica definido que, salvo indicação, é este perfil.
- A ordem entre escritas de entradas diferentes não é garantida pelo cliente: o teste usa pk nova por escrita para que a ordem não afete o oráculo.

## Passos

1. CRG-1 com 3 cenários k6 em paralelo, um por API de célula.
2. Quiescência, oráculo.
3. Repetir com CRG-2.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Distribuição | G-TRF-1 ≈ 1/3 da carga por célula; G-TRF-3 nas duas followers; G-TRF-2 só na líder ≈ soma |
| A3 | Latência por caminho | L-ACK da líder ≈ CF-01; L-ACK das followers ≈ CF-02 |
| A4 | Sem eleição e sem erro | como CF-01 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por célula de entrada, L-ACK-S, L-FWD (M-02), L-FSYNC, L-LOOP, L-TICK, L-APPLY, L-REPL.
- Golden signals: G-TRF-1..3, G-ERR-1..5, G-SAT-1..7.
- Além: B-RAFT-1..5, B-IDX, B-INV-1..3, B-DRENO, B-CUSTO.

## Prova de RPO = 0

ORC-04, separado por célula de entrada.

## Lacunas de observabilidade

M-01, M-02, M-03.

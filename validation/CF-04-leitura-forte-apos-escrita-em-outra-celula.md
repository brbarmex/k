# CF-04 · Leitura forte logo depois da escrita, em outra célula

| Campo | Valor |
|---|---|
| Categoria | Caminho feliz (complemento) |
| Origem | Complemento: RPO = 0 sem leitura correta não serve ao cliente |
| Requisitos | RPO = 0 (o dado confirmado é o dado lido), HA |
| Suporte na POC hoje | **Sim.** Toda leitura é forte: barreira `min(ReadIndex, gate)` pedida à líder e espera do apply local (`internal/usecase/read.go`, ADR-049). |
| Injeção | nenhuma |
| Duração | 2 + 10 min com CRG-3 |

## Objetivo

Provar que um GetItem forte em qualquer célula, iniciado depois de um PutItem confirmado, devolve aquele valor (ou um mais novo), e medir o custo da barreira.

## Análise crítica

- "Ler o que escrevi" não se prova com percentil nem com contadores. Precisa do verificador de linearizabilidade (ORC-06) sobre o histórico do ledger, com instantes de início e fim de cada operação.
- Na follower, a leitura paga um RPC à líder (barreira) mais a espera do apply local. Se o apply da follower está atrasado, a leitura espera até o prazo e devolve `read_timeout`: é a ligação direta entre o atraso de replicação e a disponibilidade de leitura.
- O script `k6/put-get-leader.js` lê na mesma célula em que escreveu (a líder): não exercita o caso difícil. Este cenário escreve numa célula e lê noutra.

## Passos

1. Para cada iteração: PutItem numa célula A (rodízio), e, assim que vier 200, GetItem forte numa célula B ≠ A.
2. Registrar ambos no ledger com instantes.
3. Rodar ORC-04 e ORC-06.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Toda leitura vê a escrita confirmada | ORC-06 sem violação; nenhum GetItem vazio para pk confirmada |
| A2 | RPO = 0 | ORC-04 |
| A3 | Sem `read_timeout` | `dr_reads_total{resultado="read_timeout"} == 0` |
| A4 | Custo da barreira | p99 de L-READ na follower ≤ p99 na líder + 2 × RTT p99 (hipótese) |

## Métricas

- Latência (p90, p99, p99.9, max): k6 GetItem por célula; L-READ (barreira); M-08 (leitura total por resultado); L-ACK; L-REPL.
- Golden signals: G-TRF-1 com `op="GetItem"`, G-ERR-4, G-SAT-1, G-SAT-2.
- Além: B-IDX (gate e aplicado da célula que lê), M-10 (latência do RPC de barreira).

## Prova de RPO = 0

ORC-04 para as escritas e ORC-06 para as leituras.

## Lacunas de observabilidade

M-08 (hoje só há a duração da barreira, sem o total e sem o resultado), M-10.

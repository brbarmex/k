# FC-02 · Itens grandes (até o limite de 400 KB do DynamoDB)

| Campo | Valor |
|---|---|
| Categoria | Carga (complemento) |
| Origem | Complemento (R17 do pacote: atributo de versão e limite de 400 KB) |
| Requisitos | RPO = 0, HA |
| Suporte na POC hoje | **Sim, a confirmar no limite.** O `_dr_v` acrescenta bytes ao item: um item no limite exato pode ser aceito pela camada e recusado pelo DynamoDB no apply, o que estaciona o applier (erro permanente) depois de o item já estar confirmado no log. |
| Injeção | CRG-1 com itens de 1 KiB, 64 KiB, 256 KiB e exatamente no limite (com e sem os bytes do `_dr_v`) |
| Duração | 10 min por tamanho |

## Análise crítica

- O caso no limite é um teste de RPO: um item confirmado ao cliente que o banco recusa não está em lugar nenhum além do log. Se o applier estaciona, nenhuma célula aplica nada depois dele.
- Itens grandes enchem `MaxSizePerMsg` (1 MiB) com poucas entradas e aumentam o volume de fsync: medir throughput do gp3.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Item no limite é recusado **antes** do log (400 de validação) | ledger; `dr_writes_total{resultado="recusada"}` |
| A2 | Nenhum applier estaciona | `dr_applier_stopped == 0` |
| A3 | RPO = 0 | ORC-04 |

## Métricas

- Latência (p90, p99, p99.9, max) por tamanho: L-ACK, L-FSYNC, L-APPLY.
- Golden signals: G-TRF-2, G-ERR-1, G-SAT-6 (`VolumeThroughputExceededCheck`).
- Além: B-APP, B-REDE (bytes entre células), B-CUSTO (WCU por item grande).

## Lacunas

M-07.

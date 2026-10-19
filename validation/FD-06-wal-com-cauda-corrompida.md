# FD-06 · WAL com a última gravação corrompida (queda no meio da escrita)

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento |
| Requisitos | FT, HA (a célula volta sozinha?) |
| Suporte na POC hoje | **Não (conferido no código, `eec02c9`).** O `openStorage` chama `w.ReadAll()` e, em erro, não sobe; ele não chama `wal.Repair`. No modo escrita, o `ReadAll` do etcd v3.7.2 aceita uma cauda **zerada** (a pré-alocação) e a limpa, mas devolve `io.ErrUnexpectedEOF` para uma cauda rasgada **com dados**: a célula fica em reinício em laço até intervenção manual. O `ESTADO_ATUAL.md` (3.11) foi corrigido para dizer isso. |
| Injeção | **a** natural: `aws:ec2:stop-instances` com força durante CRG-2 (repetir até ocorrer); **b** construída: com o pod parado, truncar alguns bytes do fim do último segmento do WAL (`truncate -s -N`) e subir; **c** corromper um registro no meio (inverter um byte) para ver o erro de CRC |
| Duração | até o desfecho |

## Análise crítica

- Uma escrita rasgada no fim só pode conter uma entrada que **ainda não tinha sido confirmada por esta célula** (o fsync não terminou). Cortá-la é seguro; recusar subir é perder disponibilidade sem motivo.
- Corrupção no meio (variante c) é outra coisa: perda real; a célula **não deve** subir com o mesmo ID. Os dois casos precisam de comportamentos diferentes e hoje têm o mesmo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Variante b: célula volta sozinha, cortando só a cauda | `dr_ready`; campo `ultimo` do log `partida: WAL relido` = último índice íntegro; hoje esperado **FALHAR** (sem `wal.Repair`) |
| A2 | Variante c: célula não sobe com o mesmo ID | log de erro de CRC; `CrashLoopBackOff` |
| A3 | RPO = 0 | ORC-04 |

## Métricas

- Golden signals: G-ERR-2 e G-ERR-5 na célula afetada.
- Além: B-K8S (reinícios), M-05, RTO-07.

## Lacunas

M-05; reparo da cauda na partida.

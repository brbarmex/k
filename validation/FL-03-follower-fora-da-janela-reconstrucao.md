# FL-03 · Follower fora da janela de retenção: reconstrução por cópia

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do log (complemento) |
| Origem | Complemento: é a CA-25, a DC-10 e a DC-11 da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md) |
| Requisitos | RPO = 0 (a cópia nunca regride um item), FT, HA (as escritas continuam durante a cópia) |
| Suporte na POC hoje | **Não** (Incremento B, O-40 e O-41). Hoje a célula fica parada como em FL-02. |
| Injeção | follower fora até `wal.janela[<follower>] == false` no `/status` da líder (bytes retidos desde o `Match` dela acima de `RETAIN_MAX_BYTES`); na AWS, com `RETAIN_MAX_BYTES` reduzido para caber no tempo do teste (registrar) |
| Duração | até a célula voltar a `normal` + 10 min |

## Análise crítica

- Fora da janela, nenhuma célula retém `aplicado + 1` da follower: o log não basta e ela copia a tabela de uma célula sã até F, por sobrescrita no lugar (`PutItem` condicional por `_dr_v`), e depois alcança o log desde F + 1.
- As escritas **continuam** durante a cópia: o critério central é que os itens escritos durante a cópia chegam às 3 tabelas com o mesmo `_dr_v`.
- No DynamoDB real a cópia é limitada pela WCU da tabela da follower e pelo Scan da fonte (RCU). O tempo da cópia é o "tempo em risco" deste cenário.
- Uma fonte de outra `epoch` ou `cluster_id` é recusada (DC-11): uma variante com `CLUSTER_ID` trocado numa célula (ambiente isolado) prova a recusa.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04, inclusive os itens escritos durante a cópia |
| A2 | Nada regride | ORC-04 R4: nenhum `_dr_v` menor na follower que na fonte |
| A3 | Estados | `celula.estado`: `reconstruindo` → reinício → `recuperando` → `normal` |
| A4 | Escritas seguem | RTO-08 = 0 para as entradas pela líder e pela outra follower |
| A5 | Tempo em risco | T0 → `normal`; vazão da cópia (`dr_rebuild_items_total`) |
| A6 | Pino na fonte | `wal.pinos` da fonte > 0 até o `CatchUp` da reconstruída começar (DC-10) |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK durante a cópia; L-APPLY da follower.
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-6 da fonte (o pino segura o disco dela), G-SAT-7 (WCU da tabela da follower, RCU da fonte).
- Além: `dr_rebuild_items_total`, `dr_catchup_entries_total`, B-K8S (o reinício intencional depois da cópia), B-CUSTO.

## Lacunas

Todo o Incremento B. Sem DELETE na POC (AP-19): com DELETE, este cenário precisa da lápide (`parklot/adr-tombstone.md`).

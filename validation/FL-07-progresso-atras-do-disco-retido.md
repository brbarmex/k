# FL-07 · Progresso do applier atrás do disco retido

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do log (complemento) |
| Origem | Complemento: é a DC-04 e a RT-09 da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md) |
| Requisitos | RPO = 0 (o banco da célula nunca fica sem entradas que o log já apagou) |
| Suporte na POC hoje | **Parcial, com um risco a registrar.** Incremento A: na partida, o `CommitPump` começa em `max(progresso, primeiro retido em disco)`. Isso é seguro quando o progresso só sumiu (corrompido ou ausente), porque o disco nunca é liberado além do progresso com fsync (G1). Mas, se o arquivo `progress` for **substituído por um mais antigo** (ou o banco da célula voltar no tempo), a célula pula as entradas entre o progresso e o primeiro retido, sem aviso. A partida em `reconstruindo` (DC-04) é do Incremento B (O-42). |
| Injeção | **a** apagar o arquivo `progress` com o pod parado; **b** corromper um byte dele; **c** restaurar uma cópia antiga do `progress`, tirada antes de vários snapshots; **d** restaurar a tabela da célula para um ponto anterior (PITR do DynamoDB) mantendo o volume |
| Duração | por variante, até a quiescência |

## Análise crítica

- Variantes a e b: o progresso volta a 0 e a partida reaplica a partir do primeiro retido. Tudo antes dele já estava no banco com fsync do progresso: correto e idempotente pela versão.
- Variantes c e d: o banco não tem o que o log já apagou. No Incremento A, o oráculo deve acusar divergência (R4) que o app não vê. No Incremento B, a partida deve detectar `progresso + 1 < primeiro retido` e entrar em `reconstruindo`. A variante d só detecta se o progresso também for restaurado; com o progresso intacto e o banco antigo, nem o Incremento B detecta (é o limite do P-04: backup e restauração ficam fora do produto).

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Variantes a e b: RPO = 0 sem reconstrução | ORC-04; log `partida: WAL relido`; sem erro de apply |
| A2 | Variante c, Incremento A: divergência detectada pelo oráculo | `dr_val_itens_divergentes_total > 0` (prova a lacuna) |
| A3 | Variante c, Incremento B: reconstrução automática | log `progresso %d atrás do disco retido %d: reconstrução automática`; `celula.estado == reconstruindo`; depois ORC-04 zerado |
| A4 | Variante d | registrar o desfecho; documentar como fora do escopo (P-04) |

## Métricas

- Golden signals: G-ERR-1..5, G-TRF-2.
- Além: `dr_applied_index` logo após a partida, `wal` do `/status`, B-ORC.

## Lacunas

A checagem da DC-04 (Incremento B). Detecção de banco restaurado sem o progresso (fora do produto, P-04).

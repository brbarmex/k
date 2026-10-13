# Pontos adicionais (além da lista inicial) e métricas

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 12. Pontos importantes que o Bruno não listou

Ordenados por impacto. **P0** = decidir antes de escrever código; **P1** = necessário na v1; **P2** = pode entrar em v1.1.

| # | Ponto | Por que importa | Prioridade |
|---|---|---|---|
| X1 | **Pod único por célula e fronteira `api`/`core`** (seção 3) | Disponibilidade da célula, isolamento entre as apps e a opção de separar depois. | **P0** |
| X2 | **Escopo da API DynamoDB** e fidelidade (seção 6.4/R6) | UpdateItem, condicionais e transações mudam o desenho do WAL e do applier. | **P0** |
| X3 | **Teste de compatibilidade com cada SDK/versão** usado pelos clientes | Respostas do DynamoDB têm cabeçalhos (`x-amz-crc32`, `x-amzn-RequestId`) e erros que alguns SDKs validam. Um desvio vira falha em produção, não no teste. | **P0** |
| X4 | **Formal: TLA+ do gate e do modo degradado** | O gate é a única parte do consenso fora de uma prova conhecida. | **P0** |
| X5 | **Modelo de erro e idempotência** (PEND-13, ADR-026) | O SDK retenta com `commandId` novo. Dedupe no líder, `PENDING` bem definido. | **P1** |
| X6 | **Observabilidade** | Sem métricas por etapa não se prova o orçamento de 50ms. Ver 12.1. | **P1** |
| X7 | **Segurança**: mTLS, IAM restrito, criptografia, sem dado em log | Seção 11. | **P1** |
| X8 | **Falha de fsync, corrupção e disco lento** | Seção 6.3 e 9.1. | **P1** |
| X9 | **Evolução de versão** (rolling upgrade) | O WAL tem versão de formato; o protocolo gRPC tem versão; mudanças que afetam o estado replicado precisam de *feature flag* replicada no log, senão duas versões divergem. A atualização ocorre **uma célula por vez**, igual à manutenção. | **P1** |
| X10 | **Testes de falha determinísticos** (seção 13) | A maior parte dos bugs de consenso só aparece sob falha. | **P1** |
| X11 | **Relógio**: não depender de hora de parede. Leitura forte por `ReadIndex`, não por *lease*. | Deriva de relógio entre nuvens. | **P1** |
| X12 | **Reconstrução de réplica** (6.6) | Hoje sem solução completa. | **P1** (manual) / **P2** (automática) |
| X13 | **Limites**: tamanho de item/batch, nº de shards, chaves quentes | Uma chave quente serializa o shard. | **P1** |
| X14 | **Custo**: cada escrita é aplicada **uma vez em cada célula** (3 tabelas DynamoDB na AWS + 3 contêineres Cosmos na Azure), além do log nas 6 réplicas. Estimar WCU/RU, volume de WAL, tráfego entre nuvens e IOPS (ver 6.9). | O tráfego cross-cloud é cobrado e a escrita no banco é multiplicada por 6. | **P1** |
| X15 | **Dados sensíveis / LGPD**: o WAL replica dado pessoal; retenção e apagamento. | Compliance. | **P2** |

### 12.1 Métricas mínimas (OpenTelemetry/Prometheus, endpoint de admin separado)
- Latência **por etapa** (api, hop, fsync, réplica AWS, réplica Azure, gate, apply) em histogramas.
- `commit_index`, `gate_index`, `applied_index`, e o lag de cada um por shard e por réplica.
- `wal_disk_used_ratio`, `fsync_duration`, `group_commit_batch_size`.
- `degraded_mode` (0/1), `azure_lag_entries`, `forwarded_writes_ratio` (do ADR-010), `leader_transfer_duration` (do ADR-013).
- `dedupe_hits`, `pending_responses`, `rejected_by_reason`.
- Go: `runtime/metrics` (heap, GC, goroutines), RSS e `GOMEMLIMIT`.
- Traces com amostragem baixa e `traceId` propagado da API ao applier. `pprof` apenas na porta de admin, protegida.

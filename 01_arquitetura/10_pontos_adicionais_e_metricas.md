# Pontos adicionais (além da lista inicial) e métricas

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 12. Pontos importantes que o Bruno não listou

Ordenados por impacto. **P0** = decidir antes de escrever código; **P1** = necessário na v1; **P2** = pode entrar em v1.1.

| # | Ponto | Por que importa | Prioridade |
|---|---|---|---|
| X1 | **Pod único por célula e fronteira `api`/`core`** (seção 3) | Disponibilidade da célula, isolamento entre as apps e a opção de separar depois. | **P0** |
| X2 | **Escopo da API DynamoDB** e fidelidade (seção 6.4/R6) | UpdateItem, condicionais e transações mudam o desenho do WAL e do applier. Escrita condicional está fora da v1: levantar o uso real nos clientes (PEND-15, ADR-051). | **P0** |
| X3 | **Teste de compatibilidade com cada SDK/versão** usado pelos clientes, e **suíte de conformidade** contra o DynamoDB real, a camada e o Cosmos pelo Cloud-Adapter (PEND-16, ADR-051) | Respostas do DynamoDB têm cabeçalhos (`x-amz-crc32`, `x-amzn-RequestId`) e erros que alguns SDKs validam. Um desvio vira falha em produção, não no teste. | **P0** |
| X4 | **Formal: TLA+ ampliado** (gate por termo, lease do modo degradado e promoção depois de D + M, troca de membros, dedupe, resultado desconhecido, DELETE e replay com I-DEL) | O gate é a única parte do consenso fora de uma prova conhecida, e o lease só vale se for modelado. Nasce junto com a fase 1, antes dos caminhos de banco. | **P0** |
| X5 | **Modelo de erro e idempotência** (PEND-13, ADR-026, ADR-051) | O SDK retenta com `commandId` novo. Dedupe **por requisição** no líder, `PENDING` bem definido, resultado por item (ADR-048). O limite da janela de 5 min é documentado. | **P1** |
| X6 | **Observabilidade** | Sem métricas por etapa não se prova o orçamento de 50ms. Ver 12.1. | **P1** |
| X7 | **Segurança**: mTLS (inclusive **por aplicação**, ADR-052), IAM restrito, criptografia, sem dado em log | Seção 11. A lista de permissões por aplicação evita a ampliação de privilégio pelo proxy (PEND-19). | **P1** |
| X8 | **Falha de fsync, corrupção e disco lento** | Seção 6.3 e 9.1. | **P1** |
| X9 | **Evolução de versão** (rolling upgrade) | O WAL tem versão de formato; o protocolo gRPC tem versão; mudanças que afetam o estado replicado precisam de *feature flag* replicada no log, senão duas versões divergem. A atualização ocorre **uma célula por vez**, igual à manutenção. | **P1** |
| X10 | **Testes de falha determinísticos** (seção 13) | A maior parte dos bugs de consenso só aparece sob falha. | **P1** |
| X11 | **Relógio**: não depender de hora de parede. A leitura forte usa `min(ReadIndex, gate)`, não *lease*. **A única dependência de tempo é o lease do modo degradado** (ADR-044): relógio monotônico local, validade conferida a cada ACK, margem M para pausa de processo e deriva. | Deriva e pausas de processo são o inimigo clássico do lease; medir nos clusters (H-DR13, H-DR14). | **P1** |
| X12 | **Reconstrução de réplica em tabela sombra** (6.6, ADR-047, C33) | Protocolo desenhado (F, cópia difusa, replay, cutover, checksum); assistido na v1. | **P1** (assistida) / **P2** (automática) |
| X13 | **Limites**: tamanho de item/batch, nº de shards, chaves quentes | Uma chave quente serializa o shard. | **P1** |
| X14 | **Custo**: cada escrita é aplicada **uma vez em cada célula** (3 tabelas DynamoDB na AWS + 3 contêineres Cosmos na Azure), além do log nas 6 réplicas. Estimar WCU/RU, volume de WAL, tráfego entre nuvens e IOPS (ver 6.9). Decisão de manter seis bancos: ADR-054; o custo por cliente continua sendo um número a conhecer (R20). | O tráfego cross-cloud é cobrado e a escrita no banco é multiplicada por 6; rebuild e replay têm custo de WCU/RU por célula. | **P1** |
| X15 | **Dados sensíveis / LGPD**: o WAL replica dado pessoal; retenção e apagamento. | Compliance. | **P2** |
| X16 | **Matriz automático, assistido e manual** (08, 9.5) e **RTO como distribuição medida em exercícios** | A definição de pronto só vale para as linhas automáticas; promoção, SKIP, rebuild, órfãs, prorrogação de lease e recriação da célula têm pessoa no caminho. | **P1** |
| X17 | **Elegibilidade ao failover**: limite de lag de apply do Cosmos (PEND-18) e anti-entropia por checksum (`last_reconciled`) | Sem limite, a Azure pode ser promovida com o banco muito atrás do log (R03). | **P1** |

### 12.1 Métricas mínimas (OpenTelemetry/Prometheus, endpoint de admin separado)
- Latência **por etapa** (api, hop, fsync, réplica AWS, réplica Azure, gate, apply) em histogramas, e a latência **ponta a ponta** da operação completa, que é a que o SLO mede (06, seção 7).
- **Métricas de garantia por shard** (ADR-046), sempre separadas por nome, para nunca confundir "commitado" com "garantido":
  - `last_committed`: último índice commitado pelo Raft;
  - `last_azure_durable`: 2º maior índice durável dos learners, no termo atual;
  - `last_acked`: último índice respondido ao cliente (menor ou igual ao gate);
  - `last_applied`: último índice aplicado por inteiro no banco da célula (nunca passa do gate);
  - `last_reconciled`: último índice verificado por checksum de faixa de chaves;
  - `lease_remaining`: tempo restante do lease do modo degradado, contado desde `t_c` (último contato com a maioria Azure) e zero em líder nova até contato ou `RENEW` (só `DEGRADED_POLICY=accept` e em degradado);
  - `poison_entry_index`: índice (e ordem) do item que trava o applier, por item.
  - E o lag de cada um em relação ao anterior, por shard e por réplica.
- `wal_disk_used_ratio`, `fsync_duration`, `group_commit_batch_size`.
- `degraded_mode` (0/1), `degraded_policy` (info), `azure_lag_entries`, `forwarded_writes_ratio` (do ADR-010), `leader_transfer_duration` (do ADR-013), `lease_expired_rejections` (contagem de `503 LEASE_EXPIRED`).
- `dedupe_hits`, `pending_responses`, `rejected_by_reason` (inclui `403` por falta de permissão da aplicação, `ValidationException` de `ConsistentRead` em GSI e `rejected_client_version`).
- `apply_lag_by_cell` (principalmente o Cosmos, PEND-18) e, durante um rebuild, o progresso da cópia e o lag do replay desde F.
- Go: `runtime/metrics` (heap, GC, goroutines), RSS e `GOMEMLIMIT`.
- Traces com amostragem baixa e `traceId` propagado da API ao applier. `pprof` apenas na porta de admin, protegida.

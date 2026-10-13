# Arquitetura: detalhe por assunto

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md)

Cada arquivo trata um assunto e **preserva a numeração original das seções** (5.3, 6.8, 9.4...), porque os outros documentos referenciam essas numerações. O mapa de seção para arquivo está em [00_COMECE_AQUI](../00_COMECE_AQUI.md#mapa-de-seções-da-arquitetura).

| Arquivo | Seções | Assunto |
|---|---|---|
| [00_glossario](00_glossario.md) | — | Glossário e códigos (C, X, R, ADR, PEND, H, S), incluindo lease, `DEGRADED_POLICY`, I-DEL, tabela sombra e métricas `last_*` |
| [01_forma_do_app_e_escopo](01_forma_do_app_e_escopo.md) | 2, 3 | Requisitos, metas (SLO ponta a ponta, RTO medido), premissas, app único por célula, monolito modular, porta única, mTLS por aplicação |
| [02_comunicacao_grpc](02_comunicacao_grpc.md) | 4 | gRPC entre células: por que, como usar, riscos, alternativas |
| [03_consenso_raft_quorum_e_gate](03_consenso_raft_quorum_e_gate.md) | 5, 5.3.1 a 5.3.5, 5.5.1, 5.5.2 | Qual Raft, learners e gate por termo e por evento, modo degradado com lease (D + M), `DEGRADED_POLICY`, barreira da leitura forte, os dois logs, o que pode sair antes do fsync (ADR-056) |
| [04_wal_e_fsync](04_wal_e_fsync.md) | 6.0–6.3, 6.3.1, 6.5–6.10 | Opções de WAL, desenho, progresso do applier por item, retenção (inclui desde F), reconstrução em tabela sombra, fsync, log de replicação segmentado, group commit por shard e `AsyncStorageWrites` (ADR-056) |
| [05_dados_versao_delete_ttl_gsi_e_banco_por_celula](05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md) | 6.4, 6.4.1 a 6.4.3, 6.9 | Post-image e versão, DELETE e I-DEL, TTL, Scan, Query em GSI recusada, tabela sombra e geração do nome lógico, banco por célula (ADR-054) |
| [06_leitura_forte_escrita_serial_e_latencia](06_leitura_forte_escrita_serial_e_latencia.md) | 7, 7.1, 7.2 | Orçamento e SLO ponta a ponta, modo único strong: escrita serial depois do gate e leitura forte pela barreira `min(ReadIndex, gate)` |
| [07_go_memoria_cpu_e_disco](07_go_memoria_cpu_e_disco.md) | 8 | Go: memória, CPU, disco |
| [08_resiliencia_autocura_e_timeouts](08_resiliencia_autocura_e_timeouts.md) | 9, 9.5 | Matriz de falhas do processo, probes, backpressure, prazos em cascata (com lease), matriz automático, assistido e manual |
| [09_12factors_cloud_native_e_sdks](09_12factors_cloud_native_e_sdks.md) | 10, 11, 11.2, 11.3 | 12 Factors, configuração (`DEGRADED_POLICY`, `DEGRADED_LEASE`), AKS e EKS, SDKs suportados, mTLS por aplicação, inventário e conformidade |
| [10_pontos_adicionais_e_metricas](10_pontos_adicionais_e_metricas.md) | 12 | Pontos que faltavam e métricas mínimas (inclui as de garantia por shard) |

# Arquitetura: detalhe por assunto

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md)

Cada arquivo trata um assunto e **preserva a numeração original das seções** (5.3, 6.8, 9.4...), porque os outros documentos referenciam essas numerações. O mapa de seção para arquivo está em [00_COMECE_AQUI](../00_COMECE_AQUI.md#mapa-de-seções-da-arquitetura).

| Arquivo | Seções | Assunto |
|---|---|---|
| [00_glossario](00_glossario.md) | — | Glossário e códigos (C, X, R, ADR, PEND, H, S) |
| [01_forma_do_app_e_escopo](01_forma_do_app_e_escopo.md) | 2, 3 | Requisitos, metas, premissas, app único por célula, monolito modular, porta única |
| [02_comunicacao_grpc](02_comunicacao_grpc.md) | 4 | gRPC entre células: por que, como usar, riscos, alternativas |
| [03_consenso_raft_quorum_e_gate](03_consenso_raft_quorum_e_gate.md) | 5 | Qual Raft, learners e gate, gate monotônico, os dois logs |
| [04_wal_e_fsync](04_wal_e_fsync.md) | 6.1–6.3, 6.5–6.8 | Opções de WAL, desenho, retenção, reconstrução, fsync |
| [05_dados_versao_delete_ttl_gsi_e_banco_por_celula](05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md) | 6.4, 6.4.1, 6.4.2, 6.9 | Post-image e versão, DELETE, TTL, Scan, GSI, banco por célula |
| [06_modos_de_escrita_e_leitura_e_latencia](06_modos_de_escrita_e_leitura_e_latencia.md) | 7, 7.1, 7.2 | Orçamento de latência, Strong e Eventual, leitura forte por padrão |
| [07_go_memoria_cpu_e_disco](07_go_memoria_cpu_e_disco.md) | 8 | Go: memória, CPU, disco |
| [08_resiliencia_autocura_e_timeouts](08_resiliencia_autocura_e_timeouts.md) | 9 | Matriz de falhas do processo, probes, backpressure, prazos em cascata |
| [09_12factors_cloud_native_e_sdks](09_12factors_cloud_native_e_sdks.md) | 10, 11 | 12 Factors, configuração, AKS e EKS, SDKs suportados |
| [10_pontos_adicionais_e_metricas](10_pontos_adicionais_e_metricas.md) | 12 | Pontos que faltavam e métricas mínimas |

# Comece aqui

**DataReplication v1**: uma camada entre a aplicação e o banco que só confirma a escrita depois de o dado estar durável na **AWS (DynamoDB)** e na **Azure (Cosmos DB)**, para ter **RPO = 0** em operação normal.

## Leia nesta ordem

1. **[DataReplication_Versao_1.md](DataReplication_Versao_1.md)** é o documento principal: problema, solução, decisões, arquitetura, fluxos, falhas e links para tudo o que tem mais detalhe.
2. **[02_cenarios/INDEX.md](02_cenarios/INDEX.md)** mostra cada cenário (com diagrama) e a sua situação: atualizado, complexo ou **a rever**.
3. **[06_pendencias_e_riscos/03_perguntas_para_o_bruno.md](06_pendencias_e_riscos/03_perguntas_para_o_bruno.md)** lista as decisões que dependem do Bruno, no formato `1a 2b`.
4. **[06_pendencias_e_riscos/revisao_critica_13_10.md](06_pendencias_e_riscos/revisao_critica_13_10.md)** resume a revisão crítica de 13/10 (parecer: candidata a protótipo, não a produção) e onde cada achado foi tratado.

## Para cada perfil

| Quem | Vá para |
|---|---|
| Quer entender o problema e a solução | [Documento principal](DataReplication_Versao_1.md), seções 1 e 2 |
| Decide (negócio, risco) | [Perguntas](06_pendencias_e_riscos/03_perguntas_para_o_bruno.md) e [riscos](06_pendencias_e_riscos/02_riscos.md) |
| Arquiteto | [01_arquitetura](01_arquitetura/README.md), [ADRs](03_adr/INDEX.md), [cenários complexos](02_cenarios/INDEX.md#complexos-detalhamento) |
| Vai implementar | [05_implementacao](05_implementacao/README.md) |
| Opera (runbooks futuros) | Cenários [C08 a C16](02_cenarios/INDEX.md) e [X03](02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md) |

## Mapa das pastas

| Pasta | O que tem |
|---|---|
| [`01_arquitetura/`](01_arquitetura/README.md) | Detalhe de arquitetura por assunto e o [glossário](01_arquitetura/00_glossario.md) |
| [`02_cenarios/`](02_cenarios/INDEX.md) | `atualizados/` (C01–C34), `complexos/` (X01–X08) e `a_rever/` (14 cenários ainda sem decisão completa, R03 a R23) |
| [`03_adr/`](03_adr/INDEX.md) | 54 decisões de arquitetura, um arquivo por decisão |
| [`04_diagramas/`](04_diagramas/INDEX.md) | Diagramas de arquitetura (D01–D08) e catálogo de todos os diagramas |
| [`05_implementacao/`](05_implementacao/README.md) | Guia de código, exemplos compilados, structs, testes e spikes |
| [`06_pendencias_e_riscos/`](06_pendencias_e_riscos/README.md) | Perguntas, pendências, riscos e as revisões críticas de 12 e 13/10 |

## Mapa de seções da arquitetura

Os arquivos de `01_arquitetura/` mantêm a numeração original das seções (5.3, 6.8, 9.4...). Para achar uma seção citada:

| Seção | Arquivo |
|---|---|
| 2, 3 | [01_forma_do_app_e_escopo](01_arquitetura/01_forma_do_app_e_escopo.md) |
| 4 | [02_comunicacao_grpc](01_arquitetura/02_comunicacao_grpc.md) |
| 5 (5.1 a 5.5.2) | [03_consenso_raft_quorum_e_gate](01_arquitetura/03_consenso_raft_quorum_e_gate.md) |
| 6.0 a 6.3, 6.5 a 6.8, 6.9 (log de replicação), 6.10 (group commit) | [04_wal_e_fsync](01_arquitetura/04_wal_e_fsync.md) |
| 6.4, 6.4.1, 6.4.2, 6.9 | [05_dados_versao_delete_ttl_gsi_e_banco_por_celula](01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md) |
| 7, 7.1, 7.2 | [06_leitura_forte_escrita_serial_e_latencia](01_arquitetura/06_leitura_forte_escrita_serial_e_latencia.md) |
| 8 | [07_go_memoria_cpu_e_disco](01_arquitetura/07_go_memoria_cpu_e_disco.md) |
| 9 (9.1 a 9.4) | [08_resiliencia_autocura_e_timeouts](01_arquitetura/08_resiliencia_autocura_e_timeouts.md) |
| 10, 11 | [09_12factors_cloud_native_e_sdks](01_arquitetura/09_12factors_cloud_native_e_sdks.md) |
| 12, 12.1 | [10_pontos_adicionais_e_metricas](01_arquitetura/10_pontos_adicionais_e_metricas.md) |
| 13, 14 (testes e spikes) | [05_implementacao/06_testes_e_spikes](05_implementacao/06_testes_e_spikes.md) |
| 15 (hipóteses) | [R23](02_cenarios/a_rever/R23-hipoteses-gerais.md) |
| 16 (definição de pronto) | [Documento principal, seção 11](DataReplication_Versao_1.md#11-definição-de-pronto-da-v1) |
| 17 (revisão crítica) | [revisao_critica_12_10](06_pendencias_e_riscos/revisao_critica_12_10.md) e [revisao_critica_13_10](06_pendencias_e_riscos/revisao_critica_13_10.md) |

## Como ler os status

✅ atualizado no modelo v1 · 🧩 complexo (detalhe em arquivo próprio) · ⚠️ proposta ainda não aprovada · 🟡 parcialmente tratado · ❓ sem resolução · 🆕 achado novo desta revisão.

Quando um item de `02_cenarios/a_rever/` é decidido: ele passa para `atualizados/` ou `complexos/`, ganha o ADR correspondente em `03_adr/` e a linha do índice muda.

Todos os diagramas são **Mermaid** (renderizam no GitHub, VS Code e na maioria dos visualizadores de Markdown).

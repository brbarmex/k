# Pendências, riscos e perguntas em aberto

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

| Arquivo | Conteúdo |
|---|---|
| [03_perguntas_para_o_bruno.md](03_perguntas_para_o_bruno.md) | **Comece por aqui.** Decisões que dependem do Bruno, no formato `1a 2b`, com a recomendação marcada |
| [01_pendencias.md](01_pendencias.md) | Pendências numeradas (PEND-nn), dono, padrão adotado até decidir e onde está o detalhe |
| [02_riscos.md](02_riscos.md) | Registro de riscos priorizados, com mitigação e cenário |
| [revisao_critica_12_10.md](revisao_critica_12_10.md) | Achados da revisão crítica de 12/10/2026 e onde cada um foi tratado |
| [analise_do_arquiteto_13_10.md](analise_do_arquiteto_13_10.md) | Análise ponto a ponto do arquiteto sobre o relatório de 13/10 (registro histórico, antes das decisões) |
| [analise_group_commit_e_async_storage_13_10.md](analise_group_commit_e_async_storage_13_10.md) | Análise do group commit com buffer em memória e do `AsyncStorageWrites` (13/10), base do [ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md) |
| [revisao_critica_13_10.md](revisao_critica_13_10.md) | Revisão crítica do Bruno de 13/10/2026 (parecer: candidata a protótipo), posição do arquiteto e decisões 1 a 14 |
| [analise_teste_de_carga_14_10.md](analise_teste_de_carga_14_10.md) | Teste de carga da POC de 14/10/2026: applier lento parava o laço do Raft e gerava eleições; base do [ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md) |

Cenários ainda sem decisão (detalhe e fluxos propostos): [`02_cenarios/a_rever/`](../02_cenarios/INDEX.md).

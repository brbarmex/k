# Pendências

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

| ID | Pendência | Dono | Padrão até decidir | Detalhe |
|---|---|---|---|---|
| PEND-01 | Promoção da Azure: humana ou automática (split-brain com a AWS viva). **Com os outros arquitetos; não reabrir sem a resposta do Bruno** | Bruno + arquitetos | **Só humana** ([ADR-033](../03_adr/ADR-033-promocao-da-azure-humana.md)) | [R12](../02_cenarios/a_rever/R12-promocao-com-aws-viva.md), [R06](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md) (método decidido em 13/10: modelar a regra no TLA+, [ADR-030](../03_adr/ADR-030-verificacao-formal-tla.md)) |
| PEND-02 | Aceite formal do RPO > 0 no modo degradado, sem teto. **Bloqueante** desde que o cliente passou a ser avisado por header ([ADR-027](../03_adr/ADR-027-sinalizacao-do-modo-degradado.md)) | Bruno + negócio e risco | Risco aberto | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) (H2) |
| PEND-03 | Processo de reconciliação das escritas órfãs | A definir | Extrair do log e decidir caso a caso | [R13](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md) |
| PEND-04 | Retenção do log e gatilho de reconstrução | **Decidido** ([ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)) | Teto de 70% do volume, depois Azure fora da janela e reconstrução completa; falta o tempo máximo de degradação (PEND-02) | [C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md) |
| PEND-07 | Número de escritas sequenciais por transação (com 3 ou mais, o orçamento de 50 ms não fecha) | Bruno | Assumir 1 a 2; medir por tracing | [C24](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md) |
| PEND-08 | Number com mais de 15 dígitos, Sets e Binary no Cloud-Adapter | Time do Cloud-Adapter | Valores monetários como String | [R17](../02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md) |
| PEND-11 | Critério de "líder degradada" para disparar a transferência planejada | Arquitetura | Sem disparo automático | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) (H4) |
| PEND-14 | Confirmar com a rede o plano de endereçamento sem sobreposição entre clientes (decidido 12a 13a 14a; falta a confirmação) | Rede | Premissa: faixas sem sobreposição | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) |
| PEND-12 | Limiares: `DEGRADE_AFTER`, lag de catch-up, `RESTORE_STABLE`, pausa máxima | POC | Valores do [ADR-013](../03_adr/ADR-013-transferencia-planejada-de-lideranca.md) | [R07](../02_cenarios/a_rever/R07-link-oscilando.md), [R09](../02_cenarios/a_rever/R09-resincronizacao-lenta-ou-bloqueada.md) |
| ~~PEND-13~~ | ~~Retry do SDK chega como chamada nova e pode duplicar~~ | — | **Tratado:** dedupe por id estável e prazos em cascata ([ADR-026](../03_adr/ADR-026-dedupe-de-retry-id-estavel.md), [ADR-028](../03_adr/ADR-028-timeouts-em-cascata.md)). Falta confirmar o id em cada SDK e se alguma aplicação retenta por cima do SDK | [R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) |

## Verificações pendentes (não são decisões)

- Confirmar que o identificador estável (por exemplo `amz-sdk-invocation-id`) é **o mesmo** entre as tentativas em Java 2.x, Go v2 e .NET v4.
- Confirmar se alguma aplicação retenta por cima do SDK (aumenta a janela de dedupe acima de 5 min).
- Validar `REQUEST_DEADLINE` de 7,5 s e `DEGRADE_AFTER` de 2 s em teste.
- Suposição a confirmar: os learners da Azure mantêm os dois logs.
- Modelo TLA+ do gate e de `DEGRADE`/`RESTORE` **antes** do código do gate ([ADR-030](../03_adr/ADR-030-verificacao-formal-tla.md)).

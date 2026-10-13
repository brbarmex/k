# Perguntas para o Bruno

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

Nenhuma pergunta aberta no momento. Formato das respostas: `12a 13b 14a`. A coluna **Rec.** marca a **rec**omendação (a opção que eu recomendo); qualquer item pode ser discutido antes. Cada pergunta aponta para o cenário com o raciocínio e o diagrama.

## Respondidas: consequências do "1 para 1" (12 a 14)

Respostas do Bruno em 13/10: **12a, 13a, 14a** ([ADR-043](../03_adr/ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md)). A 14a (faixas de IP sem sobreposição) é uma premissa a confirmar com a rede.

| # | Cenário | Pergunta | a | b | Rec. |
|---|---|---|---|---|---|
| 12 | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) | O lado Azure também é isolado por cliente (assinatura e conta Cosmos próprias)? | Sim, espelha a AWS: um conjunto por cliente | Não, a Azure é compartilhada e o isolamento é por banco ou resource group | **a** |
| 13 | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md), [C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) | O cabo dedicado AWS↔Azure é um só para todos os clientes. Como evitar que um cliente atrase a replicação dos outros? | Reservar ou limitar a banda por cliente (QoS no cabo e limite no `DataReplication`, com métrica por cliente) | Sem reserva: só monitorar e alertar | **a** |
| 14 | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) | As faixas de IP das VPCs e VNets de clientes diferentes são garantidamente sem sobreposição? | Sim, há um plano de endereçamento central | Não há garantia (ou não sei): a replicação precisa de NAT, overlay ou endpoints privados | verificar antes; na dúvida, **b** |

## Respondidas em 13/10/2026

| # | Pergunta | Resposta | Onde ficou |
|---|---|---|---|
| 1 | `BatchWriteItem` vira uma entrada no log? | **a** Sim, uma entrada | [ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md), [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md) |
| 2 | Applier grava com `PutItem`/`DeleteItem` condicionais por item? | **a** Condicionais individuais | [ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md), [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md) |
| 3 | O SLO de 50 ms continua excluindo a escrita no DynamoDB? | **a** Sim, exclui e publica `overhead + banco` | [ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md), [C24](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md) |
| 4 | Faixa cinzenta do link (acima da meta, abaixo de 2 s) | **a** Só alertar, RPO continua 0 | [ADR-037](../03_adr/ADR-037-faixa-cinzenta-do-link-so-alerta.md), [C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) |
| 5 | Quem aprova o `SKIP` de uma entrada venenosa? | **a** Sempre uma pessoa | [ADR-038](../03_adr/ADR-038-skip-de-entrada-venenosa-sempre-humano.md), [C26](../02_cenarios/atualizados/C26-entrada-venenosa.md) |
| 6 | Sem líder conhecida durante a eleição | **a** Esperar até 1 s no servidor e então 503 | [ADR-039](../03_adr/ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md), [C27](../02_cenarios/atualizados/C27-encaminhamento-sem-lider-conhecida.md) |
| 7 | Leitura eventual na Azure acima do limite de defasagem | **a** Redirecionar à AWS se possível, senão recusar | [ADR-040](../03_adr/ADR-040-leitura-eventual-na-azure-acima-do-limite.md), [C28](../02_cenarios/atualizados/C28-leitura-eventual-na-azure-defasada.md) |
| 8 | Disco do log em degradação longa, na v1 | **a** Só o teto de 70% e reconstrução completa | [ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), [C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md) |
| 9 | AWS que volta depois da promoção | **a** Boot cercado, revogar credenciais e recriar a célula | [ADR-042](../03_adr/ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md), [C30](../02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md) |
| 10 | Como identificar o cliente na API | **Outra resposta:** a solução é 1 para 1; o cliente é conhecido na configuração e cada time tem a sua conta AWS | [ADR-043](../03_adr/ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md), [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) |
| 11 | Regra "degradar só com 3 de 3 votantes AWS" | **a** Modelar no TLA+ e decidir com o resultado | [ADR-030](../03_adr/ADR-030-verificacao-formal-tla.md), [R06](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md) |

## Ações (não são escolhas)

- **PEND-02 (bloqueante):** levar ao negócio o aceite formal do RPO > 0 no modo degradado, com um teto de duração. Esse teto também dimensiona o volume do log ([C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)).
- **PEND-01:** aguardar a resposta dos outros arquitetos; este pacote não reabre o tema. A regra de [R06](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md) sai do modelo TLA+.
- **Verificar** o identificador estável do SDK em cada linguagem e se alguma aplicação retenta por cima do SDK ([R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md)).
- **Estimar o custo** das seis gravações por escrita lógica **por cliente**, já que cada cliente é uma instalação completa ([R20](../02_cenarios/a_rever/R20-custo-6x.md)).
- **Segundo circuito privado** por outra operadora ou cabo único na v1: decisão de custo e risco, fora do software ([C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md)).

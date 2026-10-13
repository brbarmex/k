# Perguntas para o Bruno

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

Perguntas abertas: **16 e 17** (abaixo). Formato das respostas: `16a 17a`. A coluna **Rec.** marca a **rec**omendação (a opção que eu recomendo); qualquer item pode ser discutido antes. Cada pergunta aponta para o cenário com o raciocínio e o diagrama.

## Abertas: log de replicação no Pebble (13/10)

Contexto: [ADR-055](../03_adr/ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md). Já aplicado no pacote com a recomendação; responder só se discordar.

| # | Pergunta | Opções | Rec. |
|---|---|---|---|
| 16 | Armazenamento do **log do Raft** | **a** pacote `wal` do etcd, um por shard (um fsync por shard, sem group commit entre shards; dependência pesada do módulo do etcd); **b** manter o WAL próprio segmentado só para o Raft (um fsync para todos os shards, mas o código de armazenamento volta a ser nosso) | **a** (com poucos shards, tirar o código de armazenamento próprio reduz o maior risco de perda de dado; medir k fsyncs no S1) |
| 17 | Nó que perde **só o volume do Pebble** | **a** tratar como disco perdido: volta com identidade nova ([ADR-034](../03_adr/ADR-034-fsync-fatal-e-identidade-nova.md)); **b** manter a identidade e recarregar o histórico do Pebble de um par antes de voltar a ser elegível | **a** (uma regra só para disco perdido; o b cria um estado novo de nó a modelar) |

## Respondidas: Pebble no log de replicação (13/10)

| # | Decisão | Resposta | Onde ficou |
|---|---|---|---|
| 1 | Log de replicação no Pebble | **a** Derivado do Raft, sem fsync, refeito a partir do Raft depois de queda | [ADR-055](../03_adr/ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md), [ADR-018](../03_adr/ADR-018-dois-logs-raft-e-replicacao.md), [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md) |
| 2 | Disco do Pebble | **b** Volume separado em cada nó, nas duas nuvens | [ADR-055](../03_adr/ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md), [ADR-019](../03_adr/ADR-019-wal-proprio-volume-dedicado.md) |
| 3 | Teto de 70% | **b** Mantido como está (espaço lógico); risco do espaço físico registrado | [ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), [C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md), risco 30 |

## Respondidas: revisão crítica de 13/10 (decisões 1 a 14, todas **a**)

Resposta do Bruno em 13/10: **"alternativa a para todas"**; na 11, "manter um banco por célula". Análise completa: [revisao_critica_13_10](revisao_critica_13_10.md).

| # | Decisão | Resposta | Onde ficou |
|---|---|---|---|
| 1 | Autoridade única na partição e promoção da Azure | **a** Lease D; a AWS para sozinha; a Azure só promove depois de D + M | [ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) |
| 2 | Política em modo degradado | **a** `DEGRADED_POLICY` obrigatória por instalação | [ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md) |
| 3 | Gate e prefixo | **a** Só progresso validado pelo Raft no termo atual; TLA+ ampliado | [ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md) |
| 4 | DELETE e replay | **a** DELETE real com invariante e rebuild em tabela sombra | [ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md), [C33](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md) |
| 5 | SKIP de lote | **a** Por item `(índice, ordem)` | [ADR-048](../03_adr/ADR-048-skip-por-item.md) |
| 6 | Barreira da leitura forte | **a** `min(ReadIndex, gate)` | [ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md) |
| 7 | Leitura forte em GSI | **a** Recusar `ConsistentRead` em GSI | [ADR-050](../03_adr/ADR-050-gsi-nao-tem-leitura-forte.md) |
| 8 | Idempotência e condicional | **a** Documentar o limite e levantar o uso real | [ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md) |
| 9 | Autorização por aplicação | **a** mTLS por aplicação com lista de permissões | [ADR-052](../03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md), [C34](../02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md) |
| 10 | Modo Eventual de escrita | **a** Apply paralelo fora da v1 | [ADR-053](../03_adr/ADR-053-eventual-sem-apply-paralelo-na-v1.md) |
| 11 | Banco por célula | **Manter um banco por célula** (a opção a: seis) | [ADR-054](../03_adr/ADR-054-banco-por-celula-mantido.md) |
| 12 | Esboço de código | **a** Oito defeitos corrigidos, com testes de concorrência | [05_implementacao](../05_implementacao/README.md) |
| 13 | Documentação | **a** Inconsistências corrigidas; P0 como riscos bloqueantes | [02_riscos](02_riscos.md), [Documento principal](../DataReplication_Versao_1.md) |
| 15 | Contorno de emergência para a recusa do `ForcePromote` antes de D + M | **b** Sem contorno na v1 | [ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) |
| 14 | Ordem de trabalho | **a** Cinco fases com TLA+ ampliado e núcleo mínimo verificável | [Documento principal, seção 9](../DataReplication_Versao_1.md#9-implementação) |

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

- **PEND-02 (bloqueante):** levar ao negócio o aceite formal do RPO > 0 no modo degradado **e o valor de D**, a validade do lease ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)). D também dimensiona o RTO no pior caso (D + M) e o volume do log ([C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)).
- **PEND-01:** o desenho está decidido (lease); validar com os outros arquitetos e provar no modelo TLA+. A regra de [R06](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md) sai do mesmo modelo.
- **PEND-15 e PEND-16:** inventário real de chamadas e suíte de conformidade, antes de qualquer cliente entrar.
- **PEND-19:** lista de permissões por aplicação, para o mTLS.
- **Registrar o motivo** de manter um banco por célula no [ADR-054](../03_adr/ADR-054-banco-por-celula-mantido.md) (padrão de bulkhead da organização ou escolha do projeto).
- **Verificar** o identificador estável do SDK em cada linguagem e se alguma aplicação retenta por cima do SDK ([R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md)).
- **Estimar o custo** das seis gravações por escrita lógica **por cliente**, já que cada cliente é uma instalação completa ([R20](../02_cenarios/a_rever/R20-custo-6x.md)).
- **Segundo circuito privado** por outra operadora ou cabo único na v1: decisão de custo e risco, fora do software ([C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md)).

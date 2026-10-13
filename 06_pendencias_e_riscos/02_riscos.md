# Registro de riscos

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

Gravidade pela consequência para o dado ou para a disponibilidade; a ordem é a de atenção recomendada.

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 1 | Entrada venenosa trava o shard (apply serial não pode pular) | Alta | Validação pré-commit e `SKIP` sempre por pessoa e auditado ([ADR-038](../03_adr/ADR-038-skip-de-entrada-venenosa-sempre-humano.md)) | [C26](../02_cenarios/atualizados/C26-entrada-venenosa.md) |
| 2 | Disco do log esgota em degradação longa e derruba o commit | Alta | Teto de 70% e reconstrução completa; sem S3 na v1 ([ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)) | [C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md) |
| 3 | RPO > 0 sem aceite formal e sem o valor de D (validade do lease) | Alta (negócio) | **Bloqueante** (PEND-02) | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) |
| 4 | Split-brain na promoção com a AWS viva | Alta | Mitigado por desenho: lease e promoção só depois de D + M ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)); falta D e a prova no TLA+ | [R12](../02_cenarios/a_rever/R12-promocao-com-aws-viva.md), [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) |
| 5 | Cosmos lento enche o disco do learner e degrada a AWS | Alta | Achado desta revisão; precisa de alerta e política de saída | [R03](../02_cenarios/a_rever/R03-backlog-de-apply-no-cosmos.md) |
| 6 | Bug sutil no gate/consenso customizado | Alta | Modelo TLA+ antes do código | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) (H8), [X01](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md) |
| 7 | `BatchWriteItem` não aceita condição: o applier não pode usar o lote do DynamoDB | Média | Decidido: lote = uma entrada, condicionais por item ([ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md)) | [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md) |
| 8 | Fencing do banco que volta e órfãs no DynamoDB | Média | Decidido: boot cercado, revogação e recriação da célula ([ADR-042](../03_adr/ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md)); a extração das órfãs precisa vir antes | [C30](../02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md) |
| 9 | Latência: cauda do DynamoDB no ACK e faixa cinzenta do link | Média | SLO exclui o banco e é publicado como overhead + banco ([ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)); faixa cinzenta só alerta ([ADR-037](../03_adr/ADR-037-faixa-cinzenta-do-link-so-alerta.md)); medir no spike S1 | [C24](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md), [C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) |
| 10 | Parse completo do JSON compete com heartbeat e fsync por CPU | Média | Aceito pelo Bruno; medir no S2 | [ADR-032](../03_adr/ADR-032-parse-json-completo-e-rust.md) |
| 11 | Pod único por célula: reinício derruba o endpoint da célula | Média | Partida rápida, PDB, transferência de liderança | [ADR-016](../03_adr/ADR-016-app-unico-por-celula-monolito-modular.md) |
| 12 | Custo: seis gravações por escrita lógica | Média | Estimar | [R20](../02_cenarios/a_rever/R20-custo-6x.md) |
| 13 | Memória da janela de dedupe e ABA fora da janela | Média | Achado desta revisão | [R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) |
| 14 | Dependência circular da promoção em recursos da AWS | Média | Teste com AWS bloqueada | [R11](../02_cenarios/a_rever/R11-api-de-promocao-depende-da-aws.md) |
| 15 | RTO sem número | Média | Medir | [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md) |
| 16 | Cabo dedicado compartilhado entre as instalações dos clientes: um cliente satura a banda dos outros | Alta | Decidido 13a: banda reservada ou limitada por cliente; falta implementar e medir | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md), [C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) |
| 17 | Faixas de IP sobrepostas entre contas de clientes quebram o tráfego transitivo pelo cabo | Média | Premissa de plano central (14a); confirmar com a rede | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) |
| 18 | Operar N instalações isoladas (versões, certificados, plantão de promoção) | Média | Novo (13/10); processo de frota a definir | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) |

## Riscos da revisão crítica de 13/10 (bloqueantes até haver prova)

Os itens P0 do relatório do Bruno ficam registrados como **bloqueantes**: a v1 não vai a produção sem a prova ou o teste correspondente.

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 19 | Duas pontas aceitando escrita na partição (opção B + promoção) | Alta, **bloqueante** | Lease D e promoção depois de D + M ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)); falta provar no TLA+ e aprovar D | [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) |
| 20 | Relógio do lease: pausa de processo, salto de tempo, deriva | Alta | Checagem a cada ACK e relógio monotônico; medir deriva e pausas nos clusters (H-DR13, H-DR14) | [X02](../02_cenarios/complexos/X02-modo-degradado.md) |
| 21 | Prefixo do gate e reconstrução do gate na troca de líder | Alta, **bloqueante** | Progresso só pelo Raft no termo atual ([ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)); falta o TLA+ | [X01](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md) |
| 22 | DELETE e replay sem prova; caminhos fora do checkpoint | Alta, **bloqueante** | Invariante `I-DEL` e teste de propriedade ([ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)); falta provar | [X06](../02_cenarios/complexos/X06-delete-ttl-gsi.md) |
| 23 | Reconstrução de banco sem protocolo | Alta | Protocolo em tabela sombra desenhado; falta implementar e testar com volume real | [C33](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md) |
| 24 | Compatibilidade com o DynamoDB sem inventário nem suíte; escrita condicional excluída | Alta, **bloqueante** | PEND-15 e PEND-16 ([ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)) | [R17](../02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md), [R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) |
| 25 | Ampliação de privilégio pelo proxy (qualquer pod escreve em qualquer tabela) | Alta | Decidido: mTLS por aplicação ([ADR-052](../03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md)); falta a lista de permissões (PEND-19) | [C34](../02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md) |
| 26 | Leitura forte acoplada à saúde da Azure | Média | Corrigida por desenho: `min(ReadIndex, gate)` ([ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)) | [C05](../02_cenarios/atualizados/C05-leitura-strong-e-eventual.md) |
| 27 | Semântica do Cosmos depois do failover (tamanho, `Number`, índices, TTL) | Média | Entra na suíte de conformidade (PEND-16, PEND-08) | [R17](../02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md) |
| 28 | Seis bancos por escrita: o isolamento lógico custa seis materializações, seis reconciliações e seis rebuilds | Média | Decidido manter ([ADR-054](../03_adr/ADR-054-banco-por-celula-mantido.md)); estimar o custo por cliente | [R20](../02_cenarios/a_rever/R20-custo-6x.md) |

## Riscos do log de replicação no Pebble (ADR-055, 13/10)

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 29 | Truncar o log do Raft pelo índice **copiado** para o Pebble, e não pelo coberto por um sync: uma queda de host perde a entrada naquele nó | Alta | Regra fixada no [ADR-055](../03_adr/ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md) e no [ADR-018](../03_adr/ADR-018-dois-logs-raft-e-replicacao.md); teste de queda de host simulada obrigatório | [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md) |
| 30 | O teto de 70% mede o espaço **lógico**; o Pebble só libera disco depois da compactação e precisa de espaço livre para compactar. Na degradação longa o físico pode passar do lógico, e os três votantes enchem quase juntos | Alta | **Aceito pela decisão 3b** (teto mantido como está); padrão: métrica e alerta do espaço físico, volume com folga (PEND-22). A alternativa 3a fica registrada | [C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md), [ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) |
| 31 | Checkpoint do Pebble parado (volume lento ou cheio): o Raft deixa de ser compactado e cresce no volume pequeno e na memória do `MemoryStorage` | Média | Métricas `pebble_durable_index_lag` e `raft_log_bytes` com alerta; erro do Pebble é fatal | [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md) |
| 32 | Custo e operação de **dois volumes por nó** nas duas nuvens (6 volumes a mais por instalação, por cliente); W1 com um fsync por shard; peso da dependência do etcd e memória do Pebble acima da meta de 256 MiB | Média | Volume do Raft pequeno, do Pebble em classe mais barata; medir no S1 e S2; estimar em R20 | [R20](../02_cenarios/a_rever/R20-custo-6x.md) |

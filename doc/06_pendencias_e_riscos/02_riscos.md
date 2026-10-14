# Registro de riscos

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

Gravidade pela consequência para o dado ou para a disponibilidade; a ordem é a de atenção recomendada.

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 1 | Entrada venenosa trava o shard (o applier não pode pular a entrada; com o apply paralelo, a marca d'água para nela) | Alta | Validação pré-commit e `SKIP` sempre por pessoa e auditado ([ADR-038](../03_adr/ADR-038-skip-de-entrada-venenosa-sempre-humano.md)) | [C26](../02_cenarios/atualizados/C26-entrada-venenosa.md) |
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
| 26 | Leitura forte acoplada à saúde da Azure | Média | Corrigida por desenho: `min(ReadIndex, gate)` ([ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)) | [C05](../02_cenarios/atualizados/C05-leitura-strong.md) |
| 27 | Semântica do Cosmos depois do failover (tamanho, `Number`, índices, TTL) | Média | Entra na suíte de conformidade (PEND-16, PEND-08) | [R17](../02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md) |
| 28 | Seis bancos por escrita: o isolamento lógico custa seis materializações, seis reconciliações e seis rebuilds | Média | Decidido manter ([ADR-054](../03_adr/ADR-054-banco-por-celula-mantido.md)); estimar o custo por cliente | [R20](../02_cenarios/a_rever/R20-custo-6x.md) |

## Riscos do log de replicação (ADR-055, 13/10)

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 29 | Truncar o log do Raft pelo índice **copiado** para o log de replicação, e não pelo coberto por um fsync: uma queda de host perde a entrada naquele nó | Alta | Regra fixada no [ADR-055](../03_adr/ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md) e no [ADR-018](../03_adr/ADR-018-dois-logs-raft-e-replicacao.md); `domain.RaftCompactLimit` com teste; falta o teste de queda de host simulada | [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md) |
| ~~30~~ | ~~O teto de 70% mede o espaço lógico e o Pebble só libera disco depois da compactação~~ | — | **Encerrado (13/10):** o Pebble foi trocado por log segmentado; apagar o começo remove arquivos e o disco volta na hora | [C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md) |
| 31 | Checkpoint parado (volume do log de replicação lento ou cheio): o Raft deixa de ser compactado e cresce no volume pequeno e na memória do `MemoryStorage` | Média | Métricas `replog_durable_lag` e `raft_log_bytes` com alerta; erro de fsync é fatal | [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md) |
| 32 | Custo e operação de **dois volumes por nó** nas duas nuvens (6 volumes a mais por instalação, por cliente); W1 com um fsync por shard; peso da dependência do etcd | Média | Volume do Raft pequeno, o do log de replicação em classe mais barata; medir no S1 e S2; estimar em R20 | [R20](../02_cenarios/a_rever/R20-custo-6x.md) |
| 33 | Código de armazenamento próprio no log de replicação (cauda rasgada, rotação, fsync do diretório) | Média | Fora do caminho do ACK e refeito a partir do Raft; esboço com testes em `adapter/out/replog`; falta bateria de queda de host | [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md) |

## Riscos da escrita do log do Raft (ADR-056, 13/10)

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 34 | No modo síncrono com F3, o adaptador envia antes do fsync uma mensagem que não é `MsgApp` da líder (por exemplo, `MsgAppResp` de um learner): o gate conta uma durabilidade da Azure que não existe e o RPO = 0 cai sem sinal | Alta, **bloqueante** | Regra única em `domain.CanSendBeforeSync`, com teste; teste do adaptador que falha se qualquer outra mensagem sair antes do fsync; invariante no TLA+ ampliado ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md), [ADR-030](../03_adr/ADR-030-verificacao-formal-tla.md)) | [X01](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md) |
| 35 | Fsync lento no modo síncrono congela o laço do shard: heartbeats param, seguidores iniciam eleição e o lease do modo degradado pode parecer vencido | Média (atenuado em 14/10) | `ELECTION_TICKS` configurável, acima do P99.9 de fsync (30 na POC); F7 e métrica `raft_elections_total` com motivo; o spike S1b decide o `AsyncStorageWrites`. **Atenuado pelo [ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md):** o laço não espera mais o apply; resta o fsync no laço, visível em `dr_raft_loop_duration_seconds` e `dr_raft_tick_gap_seconds` | [C21](../02_cenarios/atualizados/C21-disco-fsync-e-reconstrucao.md) |
| 36 | Um fsync por shard multiplica as IOPS e encosta na base de 3.000 do gp3 e do Premium SSD v2; aparece como cauda de latência em rajada, não como erro | Média | IOPS provisionadas, teto de shards por nó e gatilho do WAL compartilhado no S1 (PEND-23) | [R20](../02_cenarios/a_rever/R20-custo-6x.md) |
| 37 | Aplicações que hoje fazem Query em GSI quebram ao entrar na camada (400), e o uso real de GSI por cliente ainda não foi levantado | Média | Inventário de chamadas por cliente antes da entrada ([ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)); métrica `rejected_by_reason{reason="gsi"}` para achar quem ainda usa; mudar o padrão de acesso para a tabela base ([ADR-050](../03_adr/ADR-050-query-em-gsi-recusada.md)) | [ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md) |

## Riscos do apply e do checkpoint (ADR-058 e ADR-059, 14/10)

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 38 | Apply de um item por vez: com `PutItem` de 4 a 8 ms, o shard aplica de 125 a 250 itens/s, abaixo dos 375/s por shard da meta de 3.000 TPS com 8 shards; a fila cresce e entra no ACK da líder e na leitura forte das followers | Alta | Apply paralelo por chave com marca d'água contígua ([ADR-058](../03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)), com teste e benchmark na POC; medir a vazão por shard no S5 | [C24](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md) |
| 39 | Chave quente: a mesma chave é gravada uma vez por vez (até `1 / t_put` por segundo), atrasa as outras chaves do mesmo worker e, pela marca d'água contígua, o ACK do shard | Média | Métricas `dr_apply_inflight` e `dr_apply_item_duration_seconds`; limite de 1.000 WCU/s por partição do DynamoDB vale de qualquer forma; ACK esperando só o próprio item registrado como alternativa ([ADR-058](../03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)) | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) |
| 40 | Checkpoint do log de replicação segurando a trava do `Append` durante o fsync, com o `Append` na goroutine do gate: o gate e o ACK paravam a cada checkpoint | Média | Corrigido: fsync fora da trava e `Append` em goroutine própria com fila limitada ([ADR-059](../03_adr/ADR-059-checkpoint-do-log-de-replicacao-sem-trava-no-append.md)), com testes; métrica `dr_replog_sync_duration_seconds` | [X04](../02_cenarios/complexos/X04-partida-recuperacao-e-dois-logs.md) |

## Riscos do teste de carga da POC (ADR-060, 14/10)

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 41 | Banco lento numa célula parava o laço do Raft (canal de commits bloqueante) e derrubava a liderança do grupo inteiro; visto no teste de carga de 14/10 (termo de 7 para 18) | Alta | Corrigido: commit como sinal e `CommitPump` lendo por faixa ([ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)), com teste de 3.000 propostas com os consumidores parados e sem eleição; falta repetir o teste de carga | [C35](../02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md) |
| 42 | Follower com o apply atrasado vira líder e recusa escritas (`atraso_apply`) até alcançar `APPLY_LAG_MAX`: esse tempo entra no RTO | Alta | Alerta em `dr_apply_lag` das followers; preferir na transferência planejada a célula com menor atraso; medir no exercício de falha ([R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md)) | [C35](../02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md) |
| 43 | Admissão por atraso: o banco lento da líder vira indisponibilidade de escrita (ThrottlingException), e um `APPLY_LAG_MAX` baixo demais recusa em rajadas normais | Média | Padrão `APPLY_WORKERS × 256`; calibrar com a vazão medida no S5; métrica `dr_throttled_total{causa="atraso_apply"}` e transferência planejada se persistir | [C35](../02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md) |
| 44 | Entradas commitadas e não consumidas ficam na memória do Raft (`MemoryStorage`) enquanto o apply está parado; com o banco parado por muito tempo a memória cresce | Média | Na POC a compactação já estava desligada; em produção a compactação até o `DurableIndex` e o teto de disco ([ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)) limitam; métrica `dr_commit_undelivered` com alerta | [C35](../02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md) |
| 45 | Janela `nao_lider`: logo depois de uma troca de líder, a célula ainda se acha líder e a proposta é descartada (ThrottlingException em vez de encaminhar) | Baixa | Janela do tamanho de um `Ready` com o laço sem bloqueio; métrica `dr_throttled_total{causa="nao_lider"}`; o SDK repete | [C35](../02_cenarios/atualizados/C35-banco-lento-nao-derruba-a-lideranca.md) |

# Índice de ADRs

Um arquivo por decisão. ADR-001 a 015 são herdados das versões anteriores da documentação (revisados); ADR-016 a 059 registram as decisões da v1 do DataReplication (12 a 14/10/2026; os ADRs 044 a 054 vêm da revisão crítica de 13/10; o ADR-055, do log de replicação segmentado; o ADR-056, do group commit e do `AsyncStorageWrites`; o ADR-057, do modo único strong; os ADRs 058 e 059, da revisão do apply e do checkpoint em 14/10). Os números 009, 012, 023, 040 e 053 ficaram vagos: tratavam do modo Eventual, removido pelo ADR-057. O status **Provisório** indica pendência aberta.

| ADR | Título | Status |
|---|---|---|
| [ADR-001](ADR-001-camada-intermediaria-agnostica.md) | Camada intermediária agnóstica a cloud e a banco | Confirmado (requisito) |
| [ADR-002](ADR-002-shards-logicos-celulas-ativo-passivo.md) | Shards lógicos, células e ativo/passivo | Confirmado (requisito) |
| [ADR-003](ADR-003-aws-primaria-azure-failover.md) | AWS primária e Azure como failover, ambas só em São Paulo | Confirmado (restrição dura) |
| [ADR-004](ADR-004-dynamodb-primeiro-adaptador.md) | DynamoDB como primeiro adaptador; Cosmos pelo Cloud-Adapter | Confirmado (requisito) |
| [ADR-005](ADR-005-ack-exige-gate.md) | ACK só depois do gate (Raft AWS e maioria durável Azure) | Aceito (reformulado na v1) |
| [ADR-006](ADR-006-consenso-por-shard-eleicao-na-aws.md) | Consenso por shard; eleição só na AWS | Aceito |
| [ADR-007](ADR-007-sidecar-e-coordenacao-compartilhada.md) | Sidecar com coordenação compartilhada | Substituído pelo ADR-016 |
| [ADR-008](ADR-008-terceiro-dominio.md) | Terceiro domínio com WAL integral | Descartado |
| [ADR-010](ADR-010-follower-encaminha-para-lider.md) | Follower encaminha para a líder | Aceito (Bruno, 11/10/2026) |
| [ADR-011](ADR-011-witness-manifesto-snapshots.md) | Witness = manifesto assinado, snapshots e WAL | Mantido |
| [ADR-013](ADR-013-transferencia-planejada-de-lideranca.md) | Transferência planejada de liderança | Aceito (pausa como janela de exceção) |
| [ADR-014](ADR-014-modo-degradado-opcao-b.md) | Modo degradado: a AWS segue quando a Azure fica inalcançável (opção B) | Decidido (Bruno, 11/10/2026) |
| [ADR-015](ADR-015-wal-post-image-e-versao.md) | O log guarda a post-image e a versão do item | Aceito |
| [ADR-016](ADR-016-app-unico-por-celula-monolito-modular.md) | Um único app DataReplication por célula, monolito modular | Decidido (Bruno) |
| [ADR-017](ADR-017-azure-learners-e-gate.md) | Azure como learners, com gate | Decidido (Bruno) |
| [ADR-018](ADR-018-dois-logs-raft-e-replicacao.md) | Dois logs separados: o do Raft e o de replicação | Decidido (Bruno); regra de truncamento precisada pelo ADR-055 |
| [ADR-019](ADR-019-wal-proprio-volume-dedicado.md) | Log do Raft em volume de bloco dedicado; sem ACK em memória | Decidido (Bruno); revisado pelo ADR-055 |
| [ADR-020](ADR-020-raft-etcd-io-camada-multigrupo.md) | Raft: etcd-io/raft com camada multi-grupo própria | Decidido (Bruno) |
| [ADR-021](ADR-021-grpc-entre-celulas.md) | gRPC entre as células | Decidido (Bruno) |
| [ADR-022](ADR-022-banco-por-celula-nome-logico.md) | Um banco por célula; nome lógico com {cell} | Decidido (Bruno) |
| [ADR-024](ADR-024-delete-real-ttl-local-gsi.md) | DELETE real com ordem por chave; TTL local; GSI | Decidido (Bruno) |
| [ADR-025](ADR-025-escopo-da-api-v1-e-sdks.md) | Escopo da API v1 e SDKs suportados | Decidido (Bruno) |
| [ADR-026](ADR-026-dedupe-de-retry-id-estavel.md) | Dedupe de retry na líder por identificador estável | Decidido (Bruno) |
| [ADR-027](ADR-027-sinalizacao-do-modo-degradado.md) | Modo degradado sinalizado por header e métrica | Decidido (Bruno) |
| [ADR-028](ADR-028-timeouts-em-cascata.md) | Prazos em cascata dentro do timeout do SDK | Decidido (Bruno); valores a validar |
| [ADR-029](ADR-029-gate-monotonico-na-troca-de-lider.md) | O gate nunca recua; líder nova reconstrói antes de ACKar | Decidido (Bruno) |
| [ADR-030](ADR-030-verificacao-formal-tla.md) | Modelo TLA+ do gate antes do código do gate | Decidido (Bruno) |
| [ADR-031](ADR-031-hexagonal-e-bibliotecas.md) | Arquitetura hexagonal e bibliotecas | Decidido (Bruno) |
| [ADR-032](ADR-032-parse-json-completo-e-rust.md) | Parse completo do JSON na v1; Rust como alternativa registrada | Decidido (Bruno); problema conhecido |
| [ADR-033](ADR-033-promocao-da-azure-humana.md) | Promoção da Azure sempre humana, só depois de D + M | Decidido o desenho (ADR-044); PEND-01 fecha com D e o TLA+ |
| [ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md) | Falha de fsync é fatal; disco perdido volta com identidade nova | Decidido (Bruno) |
| [ADR-035](ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md) | BatchWriteItem vira uma entrada; o apply é por item e condicional | Decidido (Bruno) |
| [ADR-036](ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md) | O SLO de 50 ms exclui a escrita no banco; publicado como overhead + banco | Decidido (Bruno) |
| [ADR-037](ADR-037-faixa-cinzenta-do-link-so-alerta.md) | Link lento na faixa cinzenta: só alertar, RPO continua 0 | Decidido (Bruno) |
| [ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md) | O SKIP de uma entrada venenosa é sempre aprovado por uma pessoa | Decidido (Bruno) |
| [ADR-039](ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md) | Sem líder conhecida: esperar até 1 s no servidor e então 503 | Decidido (Bruno) |
| [ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) | Disco do log: teto de 70% e reconstrução completa; sem S3 na v1 | Decidido (Bruno) |
| [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) | AWS que volta depois da promoção: boot cercado, credenciais revogadas, célula recriada | Decidido (Bruno) |
| [ADR-043](ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md) | Uma instalação por cliente; o cliente é conhecido na configuração | Decidido (Bruno) |
| [ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md) | Modo degradado com lease: a AWS para sozinha e a Azure só promove depois de D mais margem | Decidido (Bruno); D depende da PEND-02 |
| [ADR-045](ADR-045-politica-de-degradacao-por-instalacao.md) | `DEGRADED_POLICY` obrigatória por instalação (`accept` ou `reject`) | Decidido (Bruno) |
| [ADR-046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md) | O gate só conta progresso validado pelo Raft no termo atual | Decidido (Bruno) |
| [ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md) | DELETE real com invariante formal; reconstrução em tabela sombra | Decidido (Bruno) |
| [ADR-048](ADR-048-skip-por-item.md) | O SKIP identifica o item: (índice, ordem) | Decidido (Bruno) |
| [ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md) | A leitura forte espera `min(ReadIndex, gate)` | Decidido (Bruno) |
| [ADR-050](ADR-050-query-em-gsi-recusada.md) | `ConsistentRead=true` em GSI é recusada, como no DynamoDB | Decidido (Bruno) |
| [ADR-051](ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md) | Compatibilidade com o DynamoDB: inventário, conformidade e limite da idempotência | Decidido (Bruno) |
| [ADR-052](ADR-052-autorizacao-por-aplicacao-com-mtls.md) | Autorização por aplicação com mTLS | Decidido (Bruno) |
| [ADR-054](ADR-054-banco-por-celula-mantido.md) | Um banco por célula mantido, com o trade-off registrado | Decidido (Bruno) |
| [ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md) | Log de replicação segmentado, derivado do Raft, sem fsync no caminho do ACK e em volume separado | Decidido (Bruno, 1a 2b 3b e opção a: sem Pebble); checkpoint precisado pelo ADR-059 |
| [ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md) | Group commit por shard, nada sai antes do fsync e `AsyncStorageWrites` decidido por spike | Decidido (Bruno, 1a 2a 3b 4a 5a 6a) |
| [ADR-057](ADR-057-um-unico-modo-strong-consistency.md) | Um único modo: strong consistency; leitura sempre forte, apply depois do gate | Decidido (Bruno); "serial" esclarecido pelo ADR-058 |
| [ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md) | Apply paralelo por chave, com a marca d'água contígua como `applied` | Decidido (Bruno, 1a) |
| [ADR-059](ADR-059-checkpoint-do-log-de-replicacao-sem-trava-no-append.md) | Checkpoint do log de replicação sem trava no `Append`, e `Append` fora da goroutine do gate | Decidido (Bruno, 2a) |

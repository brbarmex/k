# Inventário dos ADRs: status efetivo, cadeias e pontos a decidir

[Início](../00_COMECE_AQUI.md) · [Estado atual](../ESTADO_ATUAL.md) · [Índice dos ADRs](INDEX.md)

**Feito em:** 14/10/2026; perguntas respondidas pelo Bruno no mesmo dia (1a 2a 3a 4a 5a 6b 7a 8a) · **Cobre:** ADR-001 a ADR-063 (58 arquivos) · **Para que serve:** base do [ESTADO_ATUAL.md](../ESTADO_ATUAL.md). Cada ADR foi lido inteiro, e a coluna "Na POC" foi conferida no código de `DataReplicationApp` e no escopo da [spec da POC](../../spec/SPEC_POC_LOCAL.md) (R.3 e R.4).

Os números 009, 012, 023, 040 e 053 não existem: eram ADRs do antigo modo Eventual, removidos pelo [ADR-057](ADR-057-um-unico-modo-strong-consistency.md).

## Legenda

**Status efetivo**, que nem sempre é o que está escrito no cabeçalho do ADR:

| Status efetivo | Quer dizer |
|---|---|
| **Requisito** | Restrição ou requisito confirmado; não é escolha de desenho |
| **Vigente** | Vale como está escrito |
| **Vigente, complementado** | Vale, mas outros ADRs acrescentam regras; a regra completa está na cadeia do tema |
| **Vigente em parte** | Algum item foi revertido ou contradiz um ADR posterior; ver "Pontos a decidir" |
| **Sem uso** | Não foi revogado, mas nenhum ADR posterior nem o desenho atual o usa |
| **Substituído** / **Descartado** | Só registro histórico |
| **Pendente** | Proposta aguardando decisão do Bruno |

**Na POC:** **Sim** (implementado) · **Parcial** (parte implementada; o que falta está na observação) · **Fora** (fora do escopo da POC, R.4) · **Divergente** (a POC faz diferente do ADR) · **—** (não se aplica a código).

## Inventário

| ADR | Tema | Status efetivo | Alterado por / altera | Na POC | Observação |
|---|---|---|---|---|---|
| [001](ADR-001-camada-intermediaria-agnostica.md) | Estrutura | Requisito | — | Sim | API do DynamoDB na frente do banco |
| [002](ADR-002-shards-logicos-celulas-ativo-passivo.md) | Estrutura | Requisito | — | Parcial | 3 votantes; sem learners; um shard só. Divisão de shard aberta (H11), relevante para o [ADR-062](ADR-062-banco-com-versao-de-outra-historia-do-log.md) |
| [003](ADR-003-aws-primaria-azure-failover.md) | Estrutura | Requisito | — | — | Restrição dura: só AWS e Azure em São Paulo |
| [004](ADR-004-dynamodb-primeiro-adaptador.md) | API | Requisito | — | Parcial | API DynamoDB sim; Cosmos e Cloud-Adapter fora |
| [005](ADR-005-ack-exige-gate.md) | Escrita e gate | Vigente, complementado | 017, 029, 046; 057 (o ACK também espera o apply) | Parcial | Sem Azure, gate = commit |
| [006](ADR-006-consenso-por-shard-eleicao-na-aws.md) | Consenso | Vigente | — | Sim | |
| [007](ADR-007-sidecar-e-coordenacao-compartilhada.md) | Estrutura | Substituído | por 016 | — | |
| [008](ADR-008-terceiro-dominio.md) | Estrutura | Descartado | autoridade única vem do lease (044) | — | |
| [010](ADR-010-follower-encaminha-para-lider.md) | Operação | Vigente, complementado | 039 (um salto, termo, `WAIT_LEADER`) | Sim | Critério de "líder degradada" pendente (PEND-11). Cabeçalho fora do padrão ("Estado" em vez de "Status") |
| [011](ADR-011-witness-manifesto-snapshots.md) | Operação | **Sem uso** | marcado pela resposta 3a | — | Nenhum ADR posterior usa o witness; o snapshot virou só marcador (063) |
| [013](ADR-013-transferencia-planejada-de-lideranca.md) | Operação | Vigente, complementado | fases 2 e 3 alinhadas a 015, 029, 042, 046, 049, 057 (respostas 1a 2a) | Parcial | Só `TransferLeadership` exposto; o protocolo de 5 fases não existe |
| [014](ADR-014-modo-degradado-opcao-b.md) | Modo degradado | Vigente, complementado | 044 (lease), 045 (política), 027 | Fora | Política fixa `reject` na POC |
| [015](ADR-015-wal-post-image-e-versao.md) | Dados | Vigente, complementado | 035, 058; 062 (pendente) | Sim | `PutItem` condicional por `_dr_v` |
| [016](ADR-016-app-unico-por-celula-monolito-modular.md) | Estrutura | Vigente | substitui 007 | Sim | |
| [017](ADR-017-azure-learners-e-gate.md) | Escrita e gate | Vigente, complementado | 046 | Parcial | Gate com learners só em teste de unidade |
| [018](ADR-018-dois-logs-raft-e-replicacao.md) | Logs | Vigente, complementado | 055 (truncamento até o `DurableIndex`) | Sim | |
| [019](ADR-019-wal-proprio-volume-dedicado.md) | Logs | Vigente, complementado | 055 (WAL do etcd e log segmentado), 056 (IOPS) | Sim | Dois volumes por célula |
| [020](ADR-020-raft-etcd-io-camada-multigrupo.md) | Consenso | Vigente | — | Parcial | `etcd-io/raft` sim; camada multi-grupo não (um shard) |
| [021](ADR-021-grpc-entre-celulas.md) | Transporte | Vigente | — | Parcial | gRPC sim; **sem mTLS** (`insecure`) |
| [022](ADR-022-banco-por-celula-nome-logico.md) | Dados | Vigente, complementado | 054 (mantido), 047 (geração no nome) | Parcial | Nome lógico com `{cell}` sim; geração não |
| [024](ADR-024-delete-real-ttl-local-gsi.md) | Dados | Vigente, complementado | 047, 050, 058 | Fora | `DeleteItem` é fase 2; TTL fora |
| [025](ADR-025-escopo-da-api-v1-e-sdks.md) | API | Vigente, complementado | 051 | Parcial | PutItem, GetItem e Scan; o resto responde 400 |
| [026](ADR-026-dedupe-de-retry-id-estavel.md) | Idempotência | Vigente, complementado | 051 | Fora | |
| [027](ADR-027-sinalizacao-do-modo-degradado.md) | Modo degradado | Vigente, complementado | 044, 045 (o header não é aceite de risco) | Fora | |
| [028](ADR-028-timeouts-em-cascata.md) | Prazos | Vigente (valores a validar) | esclarecido pela resposta 5a | Sim | O limite do cliente do banco vale só no caminho da requisição; o applier repete sem limite |
| [029](ADR-029-gate-monotonico-na-troca-de-lider.md) | Escrita e gate | Vigente, complementado | 046, 049 | Fora | Sem Azure |
| [030](ADR-030-verificacao-formal-tla.md) | Verificação | Vigente, complementado | escopo ampliado por 044, 046, 047, 049, 056 | Fora | O modelo não existe no repositório. Pela resposta 8a, a regra vale para o gate com learners reais; o gate da POC (= commit) não conta |
| [031](ADR-031-hexagonal-e-bibliotecas.md) | Código | Vigente | — | Parcial | Hexagonal e bibliotecas sim; **sem `depguard`** (não há linter configurado) |
| [032](ADR-032-parse-json-completo-e-rust.md) | Código | Vigente (problema conhecido) | — | Sim | |
| [033](ADR-033-promocao-da-azure-humana.md) | Promoção | Vigente, complementado | 044 | Fora | |
| [034](ADR-034-fsync-fatal-e-identidade-nova.md) | Falhas | Vigente, complementado | 056 | Parcial | Fsync fatal sim; identidade nova e `drctl rebuild` não |
| [035](ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md) | Apply | Vigente, complementado | 048, 058 | Parcial | Apply condicional por item sim; `BatchWriteItem` é fase 2 |
| [036](ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md) | Latência | Vigente | — | — | |
| [037](ADR-037-faixa-cinzenta-do-link-so-alerta.md) | Modo degradado | Vigente | — | Fora | |
| [038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md) | Apply | Vigente, complementado | 048 | Parcial | Estaciona no item venenoso; o `SKIP` não existe |
| [039](ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md) | Operação | Vigente | complementa 010 | Sim | |
| [041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) | Logs | Vigente, complementado | 055 | Fora | Truncamento fora da POC |
| [042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) | Promoção | Vigente, complementado | 044 (gatilho); 062 propõe o mecanismo (pendente) | Fora | |
| [043](ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md) | Implantação | Vigente, complementado | 052 reverte a alternativa "mTLS por aplicação" (nota da resposta 4a) | — | |
| [044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md) | Modo degradado | Vigente (D depende da PEND-02; prova no TLA+) | — | Fora | `domain/lease.go` com testes, não ligado |
| [045](ADR-045-politica-de-degradacao-por-instalacao.md) | Modo degradado | Vigente | — | Parcial | Política fixa `reject` |
| [046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md) | Escrita e gate | Vigente | — | Parcial | `SetAzureDurable(termo, índice, learner)` no domínio, com teste; sem learners reais |
| [047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md) | Dados | Vigente | 058 (I-DEL por chave) | Fora | |
| [048](ADR-048-skip-por-item.md) | Apply | Vigente | — | Parcial | Progresso (índice, ordem) sim; `SKIP` e consulta por item não |
| [049](ADR-049-barreira-da-leitura-forte-e-o-gate.md) | Leitura | Vigente | — | Sim | Item 4 implementado em 14/10 (resposta 7a): `503 APPLIER_STOPPED` na hora |
| [050](ADR-050-query-em-gsi-recusada.md) | Leitura | Vigente | revisa a revisão crítica | Sim | `IndexName` recusado |
| [051](ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md) | API | Vigente | — | Parcial | `_dr_v` fora das respostas sim; inventário e suíte de conformidade fora |
| [052](ADR-052-autorizacao-por-aplicacao-com-mtls.md) | Segurança | Vigente | completa 043 | Fora | |
| [054](ADR-054-banco-por-celula-mantido.md) | Dados | Vigente | mantém 022 | Sim | Um LocalStack por célula desde 14/10 |
| [055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md) | Logs | Vigente, complementado | 059; revisa 018 e 019 | Parcial | Log segmentado, checkpoint e `DurableIndex` sim; compactação e truncamento não |
| [056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md) | Logs | Vigente, complementado | 060 (risco 35) | Parcial | F3 sim (`SplitBeforeSync`). Pela resposta 6b, o lote da POC vem só do `Ready`; o `raftstore.Writer` entra com o `AsyncStorageWrites` (S1b) |
| [057](ADR-057-um-unico-modo-strong-consistency.md) | Consistência | Vigente, complementado | 058 (o que "serial" quer dizer) | Sim | |
| [058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md) | Apply | Vigente | mantido depois do 061 | Sim | |
| [059](ADR-059-checkpoint-do-log-de-replicacao-sem-trava-no-append.md) | Logs | Vigente | complementa 055 | Sim | |
| [060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md) | Raft e apply | Vigente | atualiza 056 e 059 | Sim | |
| [061](ADR-061-ack-e-getitem-esperam-a-marca-dagua-do-slot-da-chave.md) | Apply | Descartado | — | — | Revertido em 14/10 |
| [062](ADR-062-banco-com-versao-de-outra-historia-do-log.md) | Dados | Pendente | complementaria 042 e 015 | — | |
| [063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md) | Raft | Pendente | fecharia o RT-11 | — | Hoje `Compact` não faz nada |

**Contagem:** 4 requisitos · 23 vigentes · 25 vigentes e complementados · 0 vigentes em parte · 1 sem uso · 1 substituído · 2 descartados · 2 pendentes.

## Cadeias: onde a regra exige ler mais de um ADR

A regra vigente de cada linha está consolidada no [ESTADO_ATUAL.md](../ESTADO_ATUAL.md).

| Tema | Ler, nesta ordem | A regra em uma linha |
|---|---|---|
| Quando sai o ACK | 005 → 017 → 029 → 046 → 057 → 058 | Commit 2/3 AWS, gate com 2/3 Azure no termo atual, apply na líder (marca d'água contígua), lease conferido |
| Os dois logs e o fsync | 018 → 019 → 055 → 056 → 059 → 060 | Só o fsync do Raft no caminho do ACK; o log de replicação é derivado, sem fsync por entrada, com checkpoint |
| Retenção e compactação | 018 → 041 → 055 → 063 (pendente) | Raft só até o `DurableIndex`; o log de replicação retém para a Azure com teto de 70% |
| Apply | 015 → 035 → 038 → 048 → 057 → 058 → 060 | Depois do gate, paralelo por chave, condicional por versão, estaciona no item venenoso, admissão por atraso |
| Leitura forte | 049 → 050 → 057 | Espera `applied ≥ min(ReadIndex, gate)`; sem GSI; sem leitura eventual |
| Modo degradado | 014 → 027 → 037 → 044 → 045 | Política por instalação; com `accept`, lease D desde `t_c`; com `reject`, nunca degrada |
| Promoção e retorno da AWS | 033 → 044 → 042 → 062 (pendente) | Humana, só depois de D + M; a AWS volta cercada e com o banco recriado |
| DELETE, TTL e GSI | 024 → 047 → 050 → 058 | DELETE real com ordem por chave; TTL local; GSI recusado; I-DEL por chave |
| Encaminhamento e liderança | 010 → 039 → 013 | Um salto, com termo; sem líder, espera 1 s e 503; transferência planejada em 5 fases |
| Identidade e segurança | 043 → 052 → 021 | Uma instalação por cliente; mTLS por aplicação com lista de permissões; mTLS entre células |
| Compatibilidade da API | 025 → 026 → 051 | Recorte da v1, dedupe por requisição em 5 min, inventário e suíte antes de cada cliente |

## Pontos a decidir (perguntas para o Bruno)

Contradições e lacunas que o inventário encontrou. **Respondidas pelo Bruno em 14/10/2026: 1a 2a 3a 4a 5a 6b 7a 8a.** Cada resposta foi registrada como "Atualização de 14/10/2026" no ADR correspondente, e o ESTADO_ATUAL já reflete todas.

1. **[Decidido: 1a] ADR-013, fase 2, item 4:** "o termo T+1 passa a ser exigido nas escritas condicionais dos appliers". O 015 e o 035 definem a condição só pela versão, e o 042 diz que a epoch é checada "no boot e periodicamente, **não por escrita**". (a) Retirar o item do 013: o fencing da líder antiga vem do Raft (ela não commita sem quórum) e do gate; (b) incluir o termo na condição de escrita.
2. **[Decidido: 2a] ADR-013, fase 3:** "as escritas voltam assim que a nova líder confirma o no-op; elas não dependem do apply". É anterior aos ADRs 029 e 046 (a líder nova reconstrói o gate antes de ACKar) e 057 (o ACK espera o apply). (a) Atualizar o texto apontando para eles; (b) manter, se a fase 3 fala de aceitar a escrita e não do ACK.
3. **[Decidido: 3a] ADR-011 (witness):** nenhum ADR posterior o usa, e o snapshot virou só marcador ([ADR-063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md)). (a) Marcar como sem uso, ou descartado; (b) manter, e dizer onde o witness entra no desenho.
4. **[Decidido: 4a] ADR-043:** a alternativa "mTLS por aplicação: custo de certificados sem ganho" foi revertida pelo 052; a atualização do 043 só cita o item 3. (a) Acrescentar a nota no 043.
5. **[Decidido: 5a] ADR-028 x AP-07:** o 028 limita o cliente do banco a "1 s por tentativa e no máximo 2"; o applier repete erro transitório **sem limite**, com espera de até 5 s ([ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md), AP-07 da spec). (a) O limite do 028 vale só no caminho da requisição (GetItem, Scan); o applier repete sem limite; (b) o applier também tem limite.
6. **[Decidido: 6b] ADR-056 na POC:** o `raftstore.Writer` (coalescência, teto de 1 MiB, respostas só depois do fsync) tem testes, mas o nó usa `wal.Save` direto no `Ready`. (a) Ligar o escritor na POC; (b) registrar que o lote da POC vem só do `Ready`, e o escritor entra com o `AsyncStorageWrites` (S1b).
7. **[Decidido: 7a] ADR-049, item 4, na POC:** com o applier estacionado, a leitura forte espera até o prazo (7,5 s) em vez de responder 503 na hora. (a) Implementar a falha rápida na POC; (b) deixar para a fase AWS.
8. **[Decidido: 8a] ADR-030 x código do gate:** o ADR pede o modelo TLA+ antes do código do gate, e o gate já tem código e testes. (a) A regra vale para o gate com learners reais (fase AWS); a POC usa gate = commit e não conta; (b) escrever o modelo antes de continuar o gate.

## Desatualizações encontradas fora dos ADRs

- `00_COMECE_AQUI.md` dizia "54 decisões"; são 58 arquivos, até o ADR-063.
- O cabeçalho do `INDEX.md` dizia "ADR-016 a 060"; vai até o 063.
- `07_site_comite/_gerador/adr_resumo.py` não tinha os ADRs 061 a 063.

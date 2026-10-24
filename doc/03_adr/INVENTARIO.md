# Inventário dos ADRs: status efetivo, cadeias e pontos a decidir

[Início](../00_COMECE_AQUI.md) · [Estado atual](../ESTADO_ATUAL.md) · [Índice dos ADRs](INDEX.md)

**Feito em:** 14/10/2026, revisto em 24/10/2026 contra o código da POC e o redesenho de 18/10; perguntas respondidas pelo Bruno no mesmo dia (1a 2a 3a 4a 5a 6b 7a 8a) · **Cobre:** ADR-001 a ADR-067 (59 arquivos) · **Para que serve:** base do [ESTADO_ATUAL.md](../ESTADO_ATUAL.md). Cada ADR foi lido inteiro, e a coluna "Na POC" foi conferida no código de `DataReplicationApp` e no escopo da [spec da POC](../../spec/SPEC_POC_LOCAL.md) (R.3 e R.4).

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
| **Em conflito** | Contradiz um ADR posterior decidido e precisa de decisão nova; ver "Revisão de 24/10/2026" |
| **Vigente, alterado** | Vale, mas um ADR posterior mudou parte da regra; a atualização de 24/10 no próprio arquivo diz o quê |

**Na POC:** **Sim** (implementado) · **Parcial** (parte implementada; o que falta está na observação) · **Fora** (fora do escopo da POC, R.4) · **Divergente** (a POC faz diferente do ADR) · **—** (não se aplica a código).

## Inventário

| ADR | Tema | Status efetivo | Alterado por / altera | Na POC | Observação |
|---|---|---|---|---|---|
| [001](ADR-001-camada-intermediaria-agnostica.md) | Estrutura | Requisito | — | Sim | API do DynamoDB na frente do banco |
| [002](ADR-002-shards-logicos-celulas-ativo-passivo.md) | Estrutura | Requisito, alterado | composição pelo 064 (3 AWS + 2 Azure votantes; só AWS 3 ou 5) | Parcial | 3 votantes AWS; um shard só. Divisão de shard aberta (H11) |
| [003](ADR-003-aws-primaria-azure-failover.md) | Estrutura | Requisito | — | — | Restrição dura: só AWS e Azure em São Paulo |
| [004](ADR-004-dynamodb-primeiro-adaptador.md) | API | Requisito | — | Parcial | API DynamoDB sim; Cosmos e Cloud-Adapter fora |
| [005](ADR-005-ack-exige-gate.md) | Escrita e gate | Vigente, complementado | 064 (gate = commit e K = 1 Azure), 029, 046; 057 (o ACK também espera o apply) | Parcial | Sem Azure, gate = commit |
| [006](ADR-006-consenso-por-shard-eleicao-na-aws.md) | Consenso | Vigente, alterado | 064 (a Azure vota, mas não se candidata) | Parcial | Só os 3 votantes AWS |
| [007](ADR-007-sidecar-e-coordenacao-compartilhada.md) | Estrutura | Substituído | por 016 | — | |
| [008](ADR-008-terceiro-dominio.md) | Estrutura | Descartado | autoridade única vem do lease (044) | — | |
| [010](ADR-010-follower-encaminha-para-lider.md) | Operação | Vigente, complementado | 039 (um salto, termo, `WAIT_LEADER`) | Sim | Critério de "líder degradada" pendente (PEND-11). Cabeçalho fora do padrão ("Estado" em vez de "Status") |
| [011](ADR-011-witness-manifesto-snapshots.md) | Operação | **Sem uso** | marcado pela resposta 3a | — | Nenhum ADR posterior usa o witness; o snapshot virou só marcador (063) |
| [013](ADR-013-transferencia-planejada-de-lideranca.md) | Operação | Vigente, complementado | fases 2 e 3 alinhadas a 015, 029, 042, 046, 049, 057 (respostas 1a 2a); pré-checagem pelo 064 | Fora | `TransferLeadership` removido em 17/10 (`9710fc3`) |
| [014](ADR-014-modo-degradado-opcao-b.md) | Modo degradado | Substituído | por 064 (AWS segue sem a Azure, automático, sem lease nem política) | Fora |  |
| [015](ADR-015-wal-post-image-e-versao.md) | Dados | Vigente, complementado | 035, 058; 062 (pendente) | Sim | `PutItem` condicional por `_dr_v` |
| [016](ADR-016-app-unico-por-celula-monolito-modular.md) | Estrutura | Vigente | substitui 007 | Sim | |
| [017](ADR-017-azure-learners-e-gate.md) | Escrita e gate | Substituído | por 064 (2 Azure votantes, K = 1) | Fora | O gate com learners em teste de unidade saiu em 17/10 |
| [019](ADR-019-wal-proprio-volume-dedicado.md) | Log do Raft | Vigente, complementado | 056 (IOPS); 065 (retenção por janela em bytes e teto duro, no lugar dos 70%) | Sim | Um volume por célula; progresso e arquivo `snapshot` no mesmo volume |
| [020](ADR-020-raft-etcd-io-camada-multigrupo.md) | Consenso | Vigente | — | Parcial | `etcd-io/raft` sim; camada multi-grupo não (um shard) |
| [021](ADR-021-grpc-entre-celulas.md) | Transporte | Vigente | — | Parcial | gRPC sim; **sem mTLS** (`insecure`) |
| [022](ADR-022-banco-por-celula-nome-logico.md) | Dados | Vigente, complementado | 054 (mantido), 047 (geração no nome) | Parcial | Nome lógico com `{cell}` sim; geração não |
| [024](ADR-024-delete-real-ttl-local-gsi.md) | Dados | Vigente, complementado | 047, 050, 058 | Fora | `DeleteItem` é fase 2; TTL fora |
| [025](ADR-025-escopo-da-api-v1-e-sdks.md) | API | Vigente, complementado | 051 | Parcial | PutItem, GetItem e Scan; o resto responde 400 |
| [026](ADR-026-dedupe-de-retry-id-estavel.md) | Idempotência | Vigente, complementado | 051 | Fora | |
| [027](ADR-027-sinalizacao-do-modo-degradado.md) | Modo degradado | Vigente, alterado | 064 (o header sinaliza o RPO > 0; sem `LEASE_EXPIRED` nem `DEGRADED_POLICY`) | Fora |  |
| [028](ADR-028-timeouts-em-cascata.md) | Prazos | Vigente (valores a validar) | esclarecido pela resposta 5a | Sim | O limite do cliente do banco vale só no caminho da requisição; o applier repete sem limite |
| [029](ADR-029-gate-monotonico-na-troca-de-lider.md) | Escrita e gate | Vigente, complementado | 046, 049; 064 (K = 1); 065 (confirmado no marcador, G6) | Parcial | `Gate.Restore` com o confirmado do marcador; sem Azure |
| [030](ADR-030-verificacao-formal-tla.md) | Verificação | Vigente, complementado | escopo: 064 a 067 no lugar do lease | Fora | O modelo não existe no repositório. Pela resposta 8a, vale para o gate com Azure real |
| [031](ADR-031-hexagonal-e-bibliotecas.md) | Código | Vigente | — | Parcial | Hexagonal e bibliotecas sim; **sem `depguard`** (não há linter configurado) |
| [032](ADR-032-parse-json-completo-e-rust.md) | Código | Vigente (problema conhecido) | — | Sim | |
| [033](ADR-033-promocao-da-azure-humana.md) | Promoção | Substituído | por 064 (promoção por comando via API, automação ou operador; risco Lb aceito) | Fora |  |
| [034](ADR-034-fsync-fatal-e-identidade-nova.md) | Falhas | Vigente em parte | 056; 066 (reconstrução automática e `/admin/reconstruir`); 067 (fail-fast do volume perdido) | Parcial | Fsync fatal sim; fail-fast na árvore local sem commit; identidade nova só com a ConfChange (parking lot) |
| [035](ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md) | Apply | Vigente, complementado | 048, 058 | Parcial | Apply condicional por item sim; `BatchWriteItem` é fase 2 |
| [036](ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md) | Latência | Vigente | — | — | |
| [037](ADR-037-faixa-cinzenta-do-link-so-alerta.md) | Modo degradado | Vigente, alterado | 064 (o limite decide a entrada no automático) | Fora |  |
| [038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md) | Apply | Vigente, complementado | 048 | Parcial | Estaciona no item venenoso; o `SKIP` não existe |
| [039](ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md) | Operação | Vigente | complementa 010 | Sim | |
| [041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md) | Log do Raft | Substituído | por 065 | — |  |
| [042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) | Promoção | **Em conflito** | 064 (gatilho por comando); 066 (DC-11 recusa outra história e nunca recria); 067 (P-05: volta pela ConfChange) | Fora | Ver ponto 9 de 24/10 |
| [043](ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md) | Implantação | Vigente, complementado | 052 reverte a alternativa "mTLS por aplicação"; 067 (a configuração só cria o grupo) | — |  |
| [044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md) | Modo degradado | Substituído | por 064 (sem lease D nem D + M) | Fora | `domain/lease.go` saiu da POC em 17/10 |
| [045](ADR-045-politica-de-degradacao-por-instalacao.md) | Modo degradado | Substituído | por 064 (sem `reject`) | Fora | Ver ponto 10 de 24/10 |
| [046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md) | Escrita e gate | Vigente | termos pelo 064 (células Azure votantes) | Fora | `SetAzureDurable` saiu em 17/10 |
| [047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md) | Dados | Vigente em parte | 058 (I-DEL por chave); 066 (sobrescrita no lugar no lugar da tabela sombra); DELETE exige lápide (parking lot) | Fora | Sem `DeleteItem` |
| [048](ADR-048-skip-por-item.md) | Apply | Vigente | — | Parcial | Progresso (índice, ordem) sim; `SKIP` e consulta por item não |
| [049](ADR-049-barreira-da-leitura-forte-e-o-gate.md) | Leitura | Vigente | — | Sim | Item 4 implementado em 14/10 (resposta 7a): `503 APPLIER_STOPPED` na hora |
| [050](ADR-050-query-em-gsi-recusada.md) | Leitura | Vigente | revisa a revisão crítica | Sim | `IndexName` recusado |
| [051](ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md) | API | Vigente | — | Parcial | `_dr_v` fora das respostas sim; inventário e suíte de conformidade fora |
| [052](ADR-052-autorizacao-por-aplicacao-com-mtls.md) | Segurança | Vigente | completa 043 | Fora | |
| [054](ADR-054-banco-por-celula-mantido.md) | Dados | Vigente | mantém 022 | Sim | Um LocalStack por célula desde 14/10 |
| [056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md) | Log do Raft | Vigente, complementado | 060 (risco 35) | Parcial | F3 sim (`SplitBeforeSync`). Pela resposta 6b, o lote da POC vem só do `Ready`; o teto de 1 MiB entra com o `AsyncStorageWrites` (S1b) |
| [057](ADR-057-um-unico-modo-strong-consistency.md) | Consistência | Vigente, complementado | 058 (o que "serial" quer dizer) | Sim | |
| [058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md) | Apply | Vigente | mantido depois do 061 | Sim | |
| [060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md) | Raft e apply | Vigente | atualiza 056; partida pelo 065 | Sim |  |
| [061](ADR-061-ack-e-getitem-esperam-a-marca-dagua-do-slot-da-chave.md) | Apply | Descartado | — | — | Revertido em 14/10 |
| [062](ADR-062-banco-com-versao-de-outra-historia-do-log.md) | Dados | Pendente | complementaria 042 e 015; mitigado em parte por 066 e 067 | Parcial | Fail-fast com tabela local com itens e recusa de outra história; `epoch` fixa em 1 |
| [063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md) | Raft | Substituído | por 065 e 066 | — | Implementado como 065 (Incremento A) |
| [064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md) | Escrita e gate | Vigente | substitui 014, 017, 033, 044, 045; altera 002, 005, 006, 027, 029, 037, 042 | Parcial | Só AWS; sem Azure, K, supressão de candidatura nem promoção; `epoch` fixa em 1 |
| [065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md) | Log do Raft | Vigente | substitui 041 e 063; altera 019 e 060 | Sim | Incremento A (`2ba474d`, `38ae207`) |
| [066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md) | Recuperação | Vigente | substitui o item 3 do 047; altera 034 e 042 | Sim (sem commit) | Incremento B na árvore local de 24/10; pontos C3, C5 a C8 abertos |
| [067](ADR-067-membros-pelo-log-do-raft-e-fail-fast-do-volume-perdido.md) | Operação | Vigente | altera 034 e 043 | Parcial | Fail-fast (DC-12) na árvore local sem commit; ConfChange não existe (`ErrMembership` pelo `PEERS`) |

**Contagem (24/10/2026):** 4 requisitos (um alterado pelo 064) · 21 vigentes (dos quais 4 são os novos 064 a 067) · 19 vigentes e complementados ou alterados · 2 vigentes em parte · 1 em conflito (042) · 1 sem uso · 8 substituídos · 2 descartados · 1 pendente (062).

## Cadeias: onde a regra exige ler mais de um ADR

A regra vigente de cada linha está consolidada no [ESTADO_ATUAL.md](../ESTADO_ATUAL.md).

| Tema | Ler, nesta ordem | A regra em uma linha |
|---|---|---|
| Quando sai o ACK | 005 → 064 → 029 → 046 → 057 → 058 | Commit do Raft (maioria) e, no multicloud, K = 1 célula Azure com fsync no termo atual; apply na líder (marca d'água contígua) |
| O log do Raft e o fsync | 019 → 056 → 060 | Um único log por nó, num volume dedicado; só o fsync do Raft no caminho do ACK; o `CommitPump` entrega as entradas commitadas da memória do Raft |
| Retenção e compactação | 019 → 065 → 066 | Snapshot-marcador a cada 10.000 entradas ou 64 MiB; RAM com teto; disco até `min(aplicado com fsync, piso, pinos, S)`; janela em bytes por réplica e teto duro; fora da janela, reconstrução por sobrescrita |
| Apply | 015 → 035 → 038 → 048 → 057 → 058 → 060 | Depois do gate, paralelo por chave, condicional por versão, estaciona no item venenoso, admissão por atraso |
| Leitura forte | 049 → 050 → 057 | Espera `applied ≥ min(ReadIndex, gate)`; sem GSI; sem leitura eventual |
| Azure fora (antigo modo degradado) | 064 → 027 → 037 | A AWS segue sozinha, automático, com RPO > 0 sinalizado; sem lease e sem `reject`; o limiar de entrada está em aberto |
| Promoção e retorno da AWS | 064 → 042 (em conflito) → 066 → 067 → 062 (pendente) | Comando via API, por automação ou operador; risco aceito de duas líderes; como a AWS volta está em aberto |
| DELETE, TTL e GSI | 024 → 047 → 050 → 058 | DELETE real com ordem por chave; TTL local; GSI recusado; I-DEL por chave |
| Encaminhamento e liderança | 010 → 039 → 013 | Um salto, com termo; sem líder, espera 1 s e 503; transferência planejada em 5 fases |
| Identidade e segurança | 043 → 067 → 052 → 021 | Uma instalação por cliente; a configuração só cria o grupo; mTLS por aplicação com lista de permissões; mTLS entre células |
| Compatibilidade da API | 025 → 026 → 051 | Recorte da v1, dedupe por requisição em 5 min, inventário e suíte antes de cada cliente |

## Pontos a decidir (perguntas para o Bruno)

Contradições e lacunas que o inventário encontrou. **Respondidas pelo Bruno em 14/10/2026: 1a 2a 3a 4a 5a 6b 7a 8a.** Cada resposta foi registrada como "Atualização de 14/10/2026" no ADR correspondente, e o ESTADO_ATUAL já reflete todas.

1. **[Decidido: 1a] ADR-013, fase 2, item 4:** "o termo T+1 passa a ser exigido nas escritas condicionais dos appliers". O 015 e o 035 definem a condição só pela versão, e o 042 diz que a epoch é checada "no boot e periodicamente, **não por escrita**". (a) Retirar o item do 013: o fencing da líder antiga vem do Raft (ela não commita sem quórum) e do gate; (b) incluir o termo na condição de escrita.
2. **[Decidido: 2a] ADR-013, fase 3:** "as escritas voltam assim que a nova líder confirma o no-op; elas não dependem do apply". É anterior aos ADRs 029 e 046 (a líder nova reconstrói o gate antes de ACKar) e 057 (o ACK espera o apply). (a) Atualizar o texto apontando para eles; (b) manter, se a fase 3 fala de aceitar a escrita e não do ACK.
3. **[Decidido: 3a] ADR-011 (witness):** nenhum ADR posterior o usa, e o snapshot virou só marcador ([ADR-063](ADR-063-snapshot-e-compactacao-do-log-do-raft.md)). (a) Marcar como sem uso, ou descartado; (b) manter, e dizer onde o witness entra no desenho.
4. **[Decidido: 4a] ADR-043:** a alternativa "mTLS por aplicação: custo de certificados sem ganho" foi revertida pelo 052; a atualização do 043 só cita o item 3. (a) Acrescentar a nota no 043.
5. **[Decidido: 5a] ADR-028 x AP-07:** o 028 limita o cliente do banco a "1 s por tentativa e no máximo 2"; o applier repete erro transitório **sem limite**, com espera de até 5 s ([ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md), AP-07 da spec). (a) O limite do 028 vale só no caminho da requisição (GetItem, Scan); o applier repete sem limite; (b) o applier também tem limite.
6. **[Decidido: 6b] ADR-056 na POC:** o nó usa `wal.Save` direto no `Ready`, sem o teto de 1 MiB por lote. (a) Implementar o teto na POC; (b) registrar que o lote da POC vem só do `Ready`, e o teto entra com o `AsyncStorageWrites` (S1b).
7. **[Decidido: 7a] ADR-049, item 4, na POC:** com o applier estacionado, a leitura forte espera até o prazo (7,5 s) em vez de responder 503 na hora. (a) Implementar a falha rápida na POC; (b) deixar para a fase AWS.
8. **[Decidido: 8a] ADR-030 x código do gate:** o ADR pede o modelo TLA+ antes do código do gate, e o gate já tem código e testes. (a) A regra vale para o gate com learners reais (fase AWS); a POC usa gate = commit e não conta; (b) escrever o modelo antes de continuar o gate.

## Desatualizações encontradas fora dos ADRs

- `00_COMECE_AQUI.md` dizia "54 decisões"; são 55 arquivos, até o ADR-063.
- O cabeçalho do `INDEX.md` dizia "ADR-016 a 060"; vai até o 063.
- `07_site_comite/_gerador/adr_resumo.py` não tinha os ADRs 061 a 063.

## Revisão de 24/10/2026: código da POC e redesenho de 18/10

Feita a pedido do Bruno ("atualize as ADRs com base no estado e comportamento atual do código"). Fontes: a árvore local da `main` em 24/10 (`0b95c5e` mais o Incremento B sem commit), a [especificação](../../spec/compactacao_do_log/especificacao.md) e as [decisões](../../spec/compactacao_do_log/decisoes.md) de 18/10, cuja seção 9 já listava os ADRs que mudavam.

**Como foi feito:** o que o Bruno decidiu em 18/10 virou quatro ADRs novos (064 a 067). Os ADRs antigos não foram apagados: cada um ganhou o status novo e uma seção "Atualização de 24/10/2026" dizendo o que mudou e o que a POC faz. A coluna "Na POC" foi conferida de novo no código.

**Pontos que precisam de decisão do Bruno** (nenhum foi decidido nesta revisão):

9. **ADR-042 em conflito.** O retorno da AWS depois da promoção diz "banco recriado"; a DC-11 ([066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md)) nunca recria e recusa fonte de outra história; a P-05 ([067](ADR-067-membros-pelo-log-do-raft-e-fail-fast-do-volume-perdido.md)) diz que a volta usa a ConfChange. Falta escolher o caminho.
10. **Fim do `reject` ([045](ADR-045-politica-de-degradacao-por-instalacao.md)).** Um cliente que exige RPO 0 sempre perde a opção de parar as escritas no corte do link. Manter assim, ou reabrir a política por instalação?
11. **Uma AZ AWS fora no 3 + 2 ([064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)).** Cada commit passa a depender de um voto Azure; com o link também fora, a AWS para. Aceitar e documentar, ou mudar a composição?
12. **Entrada no automático sem a Azure.** Qual limiar declara a Azure fora (o `DEGRADE_AFTER` de 2 s?) e se o modo vira entrada do log, que o marcador precisa carregar ([065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md)).
13. **Estado que só existe no log.** `SKIP`, dedupe e o modo de RPO > 0 não estão no marcador; precisam entrar antes de essas funções existirem.
14. **Pontos abertos da crítica do prompt SPDD** (C1, C3 a C8): em especial, uma célula em recuperação ou reconstrução pode ser eleita líder (C5), porque o código não impede a candidatura.

**Divergências entre o código e os ADRs vigentes** (esperadas, mas registradas): gRPC entre células sem TLS e com codec JSON ([021](ADR-021-grpc-entre-celulas.md)); membros pelo `PEERS` em vez de ConfChange ([067](ADR-067-membros-pelo-log-do-raft-e-fail-fast-do-volume-perdido.md)); `epoch` fixa em 1 ([064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)); o Incremento B está implementado mas **sem commit** na `main` local.

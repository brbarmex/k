# ADR-064 — Topologia 3 AWS + 2 Azure votantes, confirmação com K=1 Azure e promoção por comando

**Status:** Decidido (Bruno, 18/10/2026, rodadas 3 a 4 da compactação: Ea Fa Gb Ha, J'a Ib, K sem licença, Lb) · **Data:** 18/10/2026 · **Registrado como ADR em:** 24/10/2026 · **Substitui:** [ADR-014](ADR-014-modo-degradado-opcao-b.md), [ADR-017](ADR-017-azure-learners-e-gate.md), [ADR-033](ADR-033-promocao-da-azure-humana.md), [ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [ADR-045](ADR-045-politica-de-degradacao-por-instalacao.md) · **Altera:** [ADR-002](ADR-002-shards-logicos-celulas-ativo-passivo.md), [ADR-005](ADR-005-ack-exige-gate.md), [ADR-006](ADR-006-consenso-por-shard-eleicao-na-aws.md), [ADR-027](ADR-027-sinalizacao-do-modo-degradado.md), [ADR-029](ADR-029-gate-monotonico-na-troca-de-lider.md), [ADR-037](ADR-037-faixa-cinzenta-do-link-so-alerta.md), [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) · **Afeta o estado atual:** seções 1, 2, 3.6 e 3.7

## Contexto

Até 13/10 o desenho era 3 votantes AWS + 3 learners Azure, gate com 2 de 3 learners, modo degradado com lease D, promoção humana só depois de D + M e política `accept`/`reject` por instalação (ADRs 014, 017, 033, 044 e 045). Em 18/10, ao redesenhar a compactação do log do zero, o Bruno redefiniu a topologia e a regra de confirmação ([decisões](../../spec/compactacao_do_log/decisoes.md), rodadas 3 a 4; análise em [solucao_aws_primaria_azure_failover.md](../../spec/compactacao_do_log/solucao_aws_primaria_azure_failover.md)). A [especificação](../../spec/compactacao_do_log/especificacao.md), seção 9, listou os ADRs que mudavam, sem alterá-los. Este ADR registra o desenho vigente.

## Decisão

1. **Composições.** Só AWS: 3 ou 5 células, todas votantes. Multicloud: **3 AWS + 2 Azure, todas votantes**, com a maioria sempre na AWS (até 7 células, mantendo a maioria na AWS). A composição 2 AWS + 1 Azure foi descartada.
2. **Confirmação.** Só AWS: o commit do Raft. Multicloud: o commit do Raft **e** pelo menos **K = 1** célula Azure com fsync. O índice confirmado é `min(commit do Raft, maior índice durável entre as células Azure)`. ACK e apply só andam até ele, e ele **nunca recua**, nem na troca de líder nem depois de um reinício (vai no snapshot-marcador, [ADR-065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md)).
3. **A Azure vota, mas não se candidata.** Enquanto follower, uma célula Azure não avança o relógio de eleição. Ela só vira líder por comando de promoção.
4. **Azure inteira fora ou link cortado:** a AWS segue com a líder e escrevendo, **automaticamente**, com RPO > 0 sinalizado ao cliente. Não há licença D + M nem política `reject`. Quando o link volta, a Azure alcança pelo log ou, fora da janela, por reconstrução ([ADR-066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md)).
5. **Promoção.** Comando via API, executado por automação ou por operador, quando a AWS caiu. Escolhe a célula Azure com o maior índice durável e recria o grupo só com as células Azure, numa história nova (`epoch` + 1). Não exige cercar a AWS antes.
6. **Risco aceito (Lb):** um comando de promoção dado com a AWS viva (link cortado, AWS acessível aos clientes) cria duas líderes aceitando escrita, e a história de uma delas será descartada. Esse risco precisa aparecer explícito para a liderança.

## Alternativas consideradas

- **Learners Azure com gate 2 de 3** (ADR-017): substituído; o K=1 com votantes reduz a cauda de latência e as células na Azure.
- **Licença D + M** (ADR-044, rodada 3b, J'a): retirada na rodada 3c. A licença só impede duas líderes se a AWS parar de escrever ao fim de D, o que contradiz o automático do item 4.
- **Cercar a AWS antes de promover** (pergunta L): recusado (Lb).
- **2 AWS + 1 Azure:** descartado (Ib).

## Consequências

- **RPO.** A perda da AWS inteira em operação normal não perde ACK: a célula Azure com o maior índice durável tem tudo o que foi confirmado. Com o link já cortado, o RPO é tudo o que foi escrito desde o corte.
- **Cenário oculto: uma AZ AWS fora.** Com 3 de 5, a maioria é 3. Com uma célula AWS fora, cada commit e cada eleição precisam de um voto Azure, então passam a depender do link. Se o link também cair, a AWS fica com 2 de 5 e para de escrever. O "AWS segue sem a Azure" do item 4 só vale com as 3 células AWS de pé.
- **K=1 tolera a perda da AWS, não a da AWS mais a célula Azure que tinha o dado.** As duas células Azure podem ter índices duráveis diferentes; a promoção escolhe a maior.
- **Sem `reject`.** Um cliente que exige RPO 0 sempre (por exemplo, dado monetário) perde a opção de parar as escritas no corte do link. Se algum cliente exigir, o ADR-045 precisa voltar.
- **Entrada no automático.** O item 4 precisa de um limiar para declarar a Azure fora (hoje só existe o `DEGRADE_AFTER` de 2 s do [ADR-028](ADR-028-timeouts-em-cascata.md)) e de uma decisão sobre se o modo vira entrada do log. Em aberto.
- **Promoção por automação.** Da Azure, "AWS morta" e "link cortado" parecem iguais, que era o motivo do ADR-033. Com a automação, o risco Lb deixa de depender de um erro humano. O RTO deixa de ter o piso D + M e passa a ser o tempo até o comando.
- **AWS que volta depois da promoção.** O grupo recriado tem `epoch` nova, e a reconstrução por sobrescrita recusa uma fonte de outra história ([ADR-066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md), DC-11). O [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) (banco recriado) conflita com a regra "nunca recriar". Em aberto: a P-05 diz que a volta usa a ConfChange ([ADR-067](ADR-067-membros-pelo-log-do-raft-e-fail-fast-do-volume-perdido.md)).
- **Desvio da biblioteca.** O etcd-raft não tem votante que nunca se candidata. A supressão precisa de teste, inclusive com `PreVote` e `CheckQuorum` ligados.
- O modelo TLA+ ([ADR-030](ADR-030-verificacao-formal-tla.md)) precisa cobrir este desenho, e não o lease.

## Na POC (código em 24/10/2026)

- **Só AWS, 3 votantes** fixos pelo `PEERS`. Não há célula Azure, K, supressão de candidatura, promoção nem sinal de RPO > 0.
- O confirmado é o commit do Raft na líder; na follower, `min(max(commit local, recuperado), recebido da líder)` (`internal/domain/gate.go`, DC-02). O confirmado vai no marcador e restaura o gate na partida (`Gate.Restore`, `internal/bootstrap/app.go`).
- A `epoch` do marcador é fixa em 1 (`compactor.go`, `marker.go`): a identidade da história só passa a valer quando a promoção existir.

## Onde está o detalhe

- [decisoes.md](../../spec/compactacao_do_log/decisoes.md), rodadas 3 a 4
- [especificacao.md](../../spec/compactacao_do_log/especificacao.md), seção 2
- [solucao_aws_primaria_azure_failover.md](../../spec/compactacao_do_log/solucao_aws_primaria_azure_failover.md)

# ADR-046 — O gate só conta progresso validado pelo Raft no termo atual

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026) · **Data:** 13/10/2026

## Contexto

A revisão crítica pediu prova de que "índice N durável na Azure" significa "o mesmo prefixo do log até N". Se os learners são learners do `etcd-io/raft`, o líder só avança o progresso depois da verificação de índice e termo anteriores do `AppendEntries` (casamento de logs). O risco está em dois lugares: um canal paralelo de progresso (um número sem termo) e a reconstrução do gate pela líder nova ([ADR-029](ADR-029-gate-monotonico-na-troca-de-lider.md)), que "consulta os learners" e poderia receber o sufixo de uma líder antiga.

## Decisão

1. **O progresso da Azure só chega pelas respostas do `AppendEntries`** (índice de casamento), nunca por um canal paralelo. Um learner só responde depois do fsync.
2. `LearnerDurable()` devolve **(termo, índice, identidade do learner)**. O gate só adota valores do **termo atual** da líder.
3. A líder nova reconstrói o gate (ADR-029) **só com progresso confirmado no termo novo**; valores reportados fora de banda são ignorados até a primeira confirmação.
4. O gate é alimentado **por evento** (cada resposta de learner), não apenas quando chega um lote commitado.
5. Nomes e métricas separados: `last_committed`, `last_azure_durable`, `last_acked`, `last_applied`, `last_reconciled`.
6. O modelo TLA+ ([ADR-030](ADR-030-verificacao-formal-tla.md)) cobre também: promoção entre nuvens com lease ([ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)), troca de membros (disco substituído, identidade nova, [ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)), dedupe, resultado desconhecido, DELETE e replay ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).

## Alternativas consideradas

- Persistir o índice no log Raft: um commit a mais por atualização.
- Deixar como estava: o esboço guardava só três números, sem termo.

## Consequências

- O esboço de código muda: `SetAzureDurable` recebe termo e identidade; o gate deixa de depender do ritmo dos commits.
- Janela curta sem ACK logo depois de uma eleição, já aceita no ADR-029.

## Onde está o detalhe

- [X01-gate-e-troca-de-lider.md](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md)
- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)

# ADR-013 — Transferência planejada de liderança (sincronizar antes de promover)

**Data:** 11/10/2026 · **Estado:** aceito (pausa como janela de exceção); fases 2 e 3 alinhadas em 14/10/2026 (Bruno, respostas 1a 2a do inventário) · **Afeta o estado atual:** seção 3.6 · **Relacionados:** [ADR-010](ADR-010-follower-encaminha-para-lider.md), PEND-01

## Contexto

A liderança de um shard muda de célula quando a célula atual vai ser atualizada, derrubada ou entrar em manutenção. A manutenção é feita **uma célula por vez**. A célula alvo precisa estar sincronizada antes de assumir. Restrições: RPO=0 em regime normal, overhead ≤ 50ms por transação, ~3.000 TPS, commit com maioria AWS + maioria Azure.

Esta ADR cobre **somente** a transferência com a líder atual viva. A promoção forçada sem a líder (perda da AWS) continua em PEND-01, decidida por pessoa.

## Decisão: protocolo em 5 fases

Disparado pelo Agent do control plane via `RequestLeadershipTransfer(shardId, targetCellId, reason)`.

### Fase 0 — Pré-checagem (sem pausa)
- Trava de transferência por shard (uma por vez) e cooldown desde a última troca respeitados.
- Quórum saudável dos dois lados; célula alvo saudável e com log elegível.
- Nenhuma célula do shard em manutenção além da que vai sair.
- Se a célula que sai hospeda um votante e será destruída: **adicionar o votante substituto antes** (add-then-remove), para não perder a tolerância a falha de AZ.

### Fase 1 — Catch-up a quente (sem pausa)
- Esperar a célula alvo ficar abaixo de dois limiares, com as escritas fluindo normalmente:
  - **lag de WAL** (ex.: < 100 entradas) e
  - **lag de apply** no banco da célula alvo (ex.: < 50ms).
- Timeout de convergência (ex.: 60s). O applier alvo precisa sustentar **mais de 3.000 TPS** para convergir; se não convergir, aborta sem impacto.
- Esta fase existe para que a pausa da Fase 2 seja curta: toda a sincronização pesada acontece **antes** da pausa.

### Fase 2 — Barreira curta (pausa de escritas)
1. A líder atual para de aceitar escritas novas. O DataReplication **enfileira** as requisições até o deadline, sem rejeitar.
2. A líder registra o último índice `L` e espera a célula alvo confirmar o WAL até `L` (≈ 1 RTT, porque a Fase 1 já sincronizou).
3. A líder envia `TimeoutNow` (transferência padrão do Raft). A alvo vira líder no termo `T+1` e confirma uma entrada no-op.
4. **Fencing da líder antiga:** vem do Raft e do gate, não da escrita no banco. No termo `T+1` a líder antiga não commita (não tem quórum no termo novo), e o applier só aplica o que passou do gate, então ela não confirma nem aplica nada novo. A condição de escrita do applier continua só pela versão ([ADR-015](ADR-015-wal-post-image-e-versao.md), [ADR-035](ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md)); a epoch é conferida no boot, não por escrita ([ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md)).
- **Orçamento da pausa:** alvo ≤ 50ms; aborta se passar de um limite (ex.: 200ms). Se o `TimeoutNow` ainda não tiver sido enviado, a líder antiga simplesmente retoma.

### Fase 3 — Retomada
- **Escritas:** a nova líder volta a **aceitar** escritas assim que confirma o no-op, mas só as **confirma (ACK)** depois de reconstruir o gate com progresso do termo novo ([ADR-029](ADR-029-gate-monotonico-na-troca-de-lider.md), [ADR-046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)) e de aplicá-las no próprio banco ([ADR-057](ADR-057-um-unico-modo-strong-consistency.md)). As requisições enfileiradas são reenviadas à nova líder com o mesmo identificador estável, e o dedupe evita a duplicação ([ADR-026](ADR-026-dedupe-de-retry-id-estavel.md)).
- **Leituras fortes** só abrem depois de a nova líder reconstruir o gate, e esperam o próprio `applied` alcançar `min(ReadIndex, gate)` ([ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md)). Como a Fase 1 deixou o lag de apply baixo, a espera é curta.

### Fase 4 — Roteamento e saída
- **Só depois** de a nova liderança estar confirmada, o Agent atualiza o roteamento (GTM) para a célula nova. Enquanto isso, a célula antiga encaminha para a nova líder (ADR-010), então nenhuma requisição se perde.
- A célula antiga vira follower. Só então começa a manutenção ou a remoção (com o votante substituto já ativo, se for o caso).

## Por que nesta ordem

| Escolha | Motivo |
|---|---|
| Sincronizar **antes** da pausa | A pausa fica do tamanho de 1 RTT em vez do tamanho do lag. Com 3.000 TPS, sincronizar durante a pausa estoura o orçamento de 50ms. |
| Separar WAL em dia de banco em dia | A pausa só espera o WAL da alvo; o apply em dia vem da Fase 1. Depois da troca, escrita e leitura forte seguem as regras normais (gate reconstruído e apply), sem alongar a pausa. |
| Liderança antes do roteamento | Se a transferência abortar, o roteamento ainda aponta para a líder certa. Desfazer roteamento (GTM) é mais lento e mais arriscado. |
| Enfileirar em vez de rejeitar | Uma pausa curta vira latência extra, não erro para o cliente. |
| Add-then-remove de votante | Remover primeiro deixa 2/3 votantes na AWS sem tolerância a outra falha de AZ durante a manutenção. |

## Consequências e pendências
- Requisições durante a Fase 2 pagam até o tamanho da pausa em latência. Definir se transferências planejadas entram no SLO de P99 ou se ficam como janela de exceção.
- Métricas novas: `leader_transfer_duration` por fase, `transfer_aborts`, `queued_writes_during_barrier`, `target_apply_lag_at_barrier`.
- Testes obrigatórios: abortar em cada fase; a líder antiga tenta escrever após `T+1` (deve ser rejeitada); falha da alvo durante a Fase 2; link AWS↔Azure cai no meio da transferência.
- Limiares (100 entradas, 50ms, 60s, 200ms) são valores iniciais a calibrar nos testes.

## Atualização de 14/10/2026 (inventário dos ADRs, respostas 1a e 2a)

- **Fase 2, item 4 (1a):** até esta data o item dizia que "o termo/epoch `T+1` passa a ser exigido nas escritas condicionais dos appliers". Isso contradizia o [ADR-015](ADR-015-wal-post-image-e-versao.md), o [ADR-035](ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md) e o [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md), que definem a condição só pela versão e conferem a epoch no boot. O item foi reescrito: o fencing da líder antiga vem do Raft e do gate.
- **Fase 3 (2a):** até esta data dizia que "as escritas voltam assim que a nova líder confirma o no-op: elas não dependem do apply" e que as leituras esperam `appliedIndex ≥ L`. O texto era anterior ao [ADR-029](ADR-029-gate-monotonico-na-troca-de-lider.md), ao [ADR-046](ADR-046-gate-so-conta-progresso-validado-pelo-raft.md), ao [ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md) e ao [ADR-057](ADR-057-um-unico-modo-strong-consistency.md), e foi alinhado a eles: aceitar volta com o no-op; confirmar exige gate reconstruído e apply; a leitura espera `min(ReadIndex, gate)`.

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

A transferência planejada continua sendo o desenho do produto. Na POC, o `TransferLeadership` foi removido em 17/10 (commit `9710fc3`, código sem uso): **POC: Fora**. Com células Azure votantes ([ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)), a pré-checagem passa a falar de "maioria do Raft e K = 1 Azure", não de "quórum dos dois lados".

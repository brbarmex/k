# ADR-013 — Transferência planejada de liderança (sincronizar antes de promover)

**Data:** 11/10/2026 · **Estado:** aceito (pausa como janela de exceção) · **Relacionados:** [ADR-010](ADR-010-follower-encaminha-para-lider.md), PEND-01

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
4. **Fencing:** o termo/epoch `T+1` passa a ser exigido nas escritas condicionais dos appliers. A líder antiga não consegue mais confirmar nem aplicar.
- **Orçamento da pausa:** alvo ≤ 50ms; aborta se passar de um limite (ex.: 200ms). Se o `TimeoutNow` ainda não tiver sido enviado, a líder antiga simplesmente retoma.

### Fase 3 — Retomada
- **Escritas** voltam assim que a nova líder confirma o no-op: elas não dependem do apply. As requisições enfileiradas são reenviadas à nova líder com o mesmo `commandId` (sem duplicação).
- **Leituras fortes** só abrem quando o banco da nova líder tiver `appliedIndex ≥ L`. Como a Fase 1 deixou o lag de apply baixo, a espera é curta.

### Fase 4 — Roteamento e saída
- **Só depois** de a nova liderança estar confirmada, o Agent atualiza o roteamento (GTM) para a célula nova. Enquanto isso, a célula antiga encaminha para a nova líder (ADR-010), então nenhuma requisição se perde.
- A célula antiga vira follower. Só então começa a manutenção ou a remoção (com o votante substituto já ativo, se for o caso).

## Por que nesta ordem

| Escolha | Motivo |
|---|---|
| Sincronizar **antes** da pausa | A pausa fica do tamanho de 1 RTT em vez do tamanho do lag. Com 3.000 TPS, sincronizar durante a pausa estoura o orçamento de 50ms. |
| Separar WAL em dia de banco em dia | As escritas voltam com o WAL; as leituras fortes esperam o apply. Misturar os dois alonga a pausa sem ganho de segurança. |
| Liderança antes do roteamento | Se a transferência abortar, o roteamento ainda aponta para a líder certa. Desfazer roteamento (GTM) é mais lento e mais arriscado. |
| Enfileirar em vez de rejeitar | Uma pausa curta vira latência extra, não erro para o cliente. |
| Add-then-remove de votante | Remover primeiro deixa 2/3 votantes na AWS sem tolerância a outra falha de AZ durante a manutenção. |

## Consequências e pendências
- Requisições durante a Fase 2 pagam até o tamanho da pausa em latência. Definir se transferências planejadas entram no SLO de P99 ou se ficam como janela de exceção.
- Métricas novas: `leader_transfer_duration` por fase, `transfer_aborts`, `queued_writes_during_barrier`, `target_apply_lag_at_barrier`.
- Testes obrigatórios: abortar em cada fase; a líder antiga tenta escrever após `T+1` (deve ser rejeitada); falha da alvo durante a Fase 2; link AWS↔Azure cai no meio da transferência.
- Limiares (100 entradas, 50ms, 60s, 200ms) são valores iniciais a calibrar nos testes.

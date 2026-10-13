# C13 — Ressincronização da Azure

**Status:** ✅ Atualizado (modelo v1) · **ADRs:** [ADR-014](../../03_adr/ADR-014-modo-degradado-opcao-b.md), [ADR-044](../../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [ADR-047](../../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md), [ADR-018](../../03_adr/ADR-018-dois-logs-raft-e-replicacao.md), [ADR-034](../../03_adr/ADR-034-fsync-fatal-e-identidade-nova.md) · **Relacionados:** [X02 Modo degradado](../complexos/X02-modo-degradado.md)

## Cenário

A Azure (ou o link) volta. Os learners pedem as entradas a partir do último índice durável que têm, e o catch-up é assíncrono. Com o lag abaixo do limiar e histerese, a líder propõe `RESTORE(epoch)`, o modo degradado e o lease terminam e o gate volta a exigir a maioria Azure. Se o lease já tinha vencido (`503 LEASE_EXPIRED`), as escritas só voltam com o `RESTORE` (ou com um `RENEW` humano). Se a retenção do log na AWS não cobrir o período, a Azure precisa de reconstrução manual assistida (`drctl rebuild`), que segue o protocolo da tabela sombra ([C33](C33-reconstrucao-em-tabela-sombra.md)).

## Diagrama

```mermaid
sequenceDiagram
    autonumber
    participant L as Líder AWS
    participant AZ as Learners Azure
    participant CDB as Cosmos DB
    participant OPS as Operação
    AZ->>L: reconecta com o último índice durável N-1
    alt log de replicação ainda retém a partir de N
        loop catch-up assíncrono
            L->>AZ: lote de entradas
            AZ->>CDB: apply em ordem
            AZ-->>L: índice durável do lote
        end
    else retenção excedida
        L->>OPS: Azure fora da janela, reconstrução manual
        OPS->>AZ: drctl rebuild em tabela sombra, ver C33
    end
    Note over L,AZ: lag abaixo do limiar por um tempo estável
    L->>L: propõe RESTORE epoch, o lease deixa de valer
    L->>OPS: modo normal, Azure elegível, RPO 0
```

## Etapas

1. Os learners reconectam informando o último índice durável.
2. Se o log de replicação ainda tem as entradas, a líder as envia em lotes; a Azure grava e aplica no Cosmos em ordem.
3. Se a retenção foi excedida, a Azure é marcada fora da janela e a reconstrução é manual.
4. Com o lag abaixo do limiar de forma estável (histerese), a líder propõe `RESTORE`.
5. O RPO volta a 0 e a Azure volta a ser elegível para promoção. Se a Azure já tiver sido promovida (passou D + M e a epoch mudou), não há `RESTORE`: a AWS é tratada como em [C16](C16-aws-volta-apos-promocao.md). A elegibilidade para o failover depende também do limite de lag de apply do Cosmos (PEND-18), a definir.

## Falhas e tratativas

- **Catch-up mais lento que a entrada nova.** O catch-up do log (em bytes) é rápido e é o único que bloqueia o RESTORE. O atraso de apply no Cosmos não bloqueia o RESTORE, mas afeta o RTO: [R03](../a_rever/R03-backlog-de-apply-no-cosmos.md).
- **Azure não alcança a AWS.** Parcialmente tratado: [R09](../a_rever/R09-resincronizacao-lenta-ou-bloqueada.md).
- **Lease vencido e a Azure volta.** O `RESTORE` encerra o degradado; ele não pode competir com uma promoção. Se a Azure estiver bloqueada para promoção (`promotion_blocked`, H-DR13), a ordem entre `RENEW`, `RESTORE` e o desbloqueio é definida no modelo TLA+ ([C32](C32-particao-e-lease-do-modo-degradado.md)).

## Pontos em aberto

- Reconstrução automática de réplica (H-DR1): [R23](../a_rever/R23-hipoteses-gerais.md).

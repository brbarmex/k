# C08 — Queda da célula líder ou de uma AZ na AWS

**Status:** ✅ Atualizado (modelo v1) · **ADRs:** [ADR-006](../../03_adr/ADR-006-consenso-por-shard-eleicao-na-aws.md), [ADR-029](../../03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md), [ADR-046](../../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md), [ADR-049](../../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md), [ADR-017](../../03_adr/ADR-017-azure-learners-e-gate.md) · **Relacionados:** [X01 Gate e troca de líder](../complexos/X01-gate-e-troca-de-lider.md)

## Cenário

A célula líder A1 (ou a AZ dela) cai. Os votantes A2 e A3 são maioria: um deles vence a eleição no termo T+1. Escritas sem commit são descartadas e o cliente retenta. Tudo que estava confirmado está em A2 ou A3 e na maioria da Azure. **A líder nova só ACKa depois de reconstruir o gate** (ADR-029), e só com progresso da Azure **confirmado no termo novo** (ADR-046); valores reportados fora de banda são ignorados até a primeira confirmação. RPO = 0, com pausa de escrita igual ao timeout de eleição mais a reconstrução (meta de 3 a 4 s, a medir).

## Diagrama

```mermaid
sequenceDiagram
    autonumber
    participant A1 as A1 lider termo T
    participant A2 as A2 votante
    participant A3 as A3 votante
    participant Z as Learners Azure
    A1-xA1: célula ou AZ cai
    A2->>A2: sem heartbeat, PreVote
    A2->>A3: RequestVote termo T+1
    A3-->>A2: voto concedido
    Note over A2: A2 vira lider com 2 de 3 votantes
    A2->>A3: no-op termo T+1
    A2->>Z: AppendEntries com o no-op do termo T+1
    Z-->>A2: resposta com termo T+1, índice durável e identidade do learner
    Note over A2: só conta respostas do termo novo, reconstrói o gate sem recuar e passa a ACKar e a atender leitura forte
```

## Etapas

1. **Falha:** a célula líder cai.
2. **Detecção:** A2 deixa de receber heartbeat e inicia PreVote.
3. **Eleição:** A3 concede o voto porque o log de A2 é elegível; A2 vira líder no termo T+1.
4. **Afirmação:** A2 grava um no-op do novo termo nos votantes e nos learners.
5. **Gate:** A2 adota o 2º maior índice durável entre os learners que **responderam ao `AppendEntries` no termo T+1** (índice, termo e identidade); o gate nunca recua e é alimentado por evento, a cada resposta. Um learner que reporta um índice por outro canal, ou um sufixo de uma líder antiga, é ignorado até confirmar no termo novo.
6. **Retomada:** A2 passa a ACKar, libera o applier e só então atende leitura forte (barreira `min(ReadIndex, gate)`, [C05](C05-leitura-strong.md)). As respostas informam a nova líder e o termo.

## Falhas e tratativas

- **Learner sem resposta no termo novo.** Se nenhum learner confirmar, o gate fica onde estava e a escrita espera; passados `DEGRADE_AFTER`, vale a `DEGRADED_POLICY` ([C12](C12-modo-degradado.md)). Se o shard estava em modo degradado, a líder nova herda o `DEGRADE` por replicação mas não herda `t_c`: trata o lease como expirado até obter contato com a Azure ou um `RENEW`, e fica sem escrita até lá ([ADR-044](../../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [C32](C32-particao-e-lease-do-modo-degradado.md)).
- **Líder oscilando (eleições repetidas).** PreVote e CheckQuorum. Um nó com rede instável não derruba a liderança sozinho.
- **Duas AZs da AWS caem.** Sem maioria na AWS o shard para: [R06](../a_rever/R06-duas-azs-aws-caem.md).
- **Líder lenta mas viva.** Heartbeat saudável com disco ruim: o watchdog de fsync transfere a liderança. O critério completo é a hipótese [R23](../a_rever/R23-hipoteses-gerais.md).

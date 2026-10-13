# C29 — Disco do log esgota durante uma degradação longa (antes R08)

**Status:** ✅ Atualizado (decidido pelo Bruno em 13/10/2026) · **ADRs:** [ADR-041](../../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), [ADR-019](../../03_adr/ADR-019-wal-proprio-volume-dedicado.md), [ADR-018](../../03_adr/ADR-018-dois-logs-raft-e-replicacao.md), [ADR-014](../../03_adr/ADR-014-modo-degradado-opcao-b.md) · **Relacionados:** [C12](C12-modo-degradado.md), [C21](C21-disco-fsync-e-reconstrucao.md), [X02](../complexos/X02-modo-degradado.md)

## Cenário

No modo degradado, a AWS retém todas as entradas desde `fromIndex` para a Azure fazer catch-up. A 3.000 TPS e ~2 KB por entrada são **~0,5 TB por dia por shard**. Se os votantes ficarem sem disco, o fsync falha e **não há mais commit**: o modo criado para manter o sistema de pé o derrubaria.

## Decisão

A retenção para a Azure tem **teto de 70% do volume**. Passado o teto, a Azure vira "fora da janela" e volta por **reconstrução completa**. **Sem descarga para S3 na v1.**

## Diagrama: teto de retenção

```mermaid
flowchart TD
    A["Modo degradado por horas ou dias"] --> B["Log de replicação cresce no volume dedicado"]
    B --> C{"Ocupação acima de 70 por cento?"}
    C -->|"não"| D["Segue, Azure continua elegível ao catch-up"]
    C -->|"sim"| E["Azure marcada como fora da janela, retenção liberada"]
    E --> F["Reconstrução completa da Azure quando voltar: drctl rebuild"]
```

## Etapas

1. **Alertas** de ocupação por votante com **projeção de tempo até encher**, e dimensionamento do volume para a maior degradação aceita (PEND-02).
2. Com 70% de um volume de, por exemplo, 2 TB, a retenção para a Azure cobre pouco menos de 3 dias a 3.000 TPS. O número real depende do tamanho médio da entrada, medido em teste.
3. O log do Raft é curto e **independe** desse crescimento; só o log de replicação cresce ([ADR-018](../../03_adr/ADR-018-dois-logs-raft-e-replicacao.md)).
4. Passado o teto, o log antigo é liberado e a Azure só volta por `drctl rebuild`.

## Falhas e tratativas

- **Janela perdida e a AWS cai.** Depois do teto, a Azure não alcança a AWS pelo log. Se a AWS cair nesse período, a promoção da Azure perde tudo o que foi escrito desde `fromIndex` ([R13](../a_rever/R13-escritas-orfas-reconciliacao.md)). O RPO > 0 do modo degradado não tem teto de tempo enquanto o PEND-02 não for aceito.
- **Custo e duração da reconstrução completa.** Copiar todo o conteúdo para o Cosmos consome RU e leva um tempo ainda não medido ([R09](../a_rever/R09-resincronizacao-lenta-ou-bloqueada.md), [R18](../a_rever/R18-rto-nao-quantificado.md)).
- **Disco do learner.** O mesmo risco vale do lado Azure quando o apply atrasa ([R03](../a_rever/R03-backlog-de-apply-no-cosmos.md)).
- **S3 descartado na v1.** O catch-up lendo do S3 atravessaria o link, exigiria credencial AWS na Azure ([R23](../a_rever/R23-hipoteses-gerais.md), H10) e pioraria o desastre em que a AWS some.

## Pontos em aberto

- Tempo máximo de degradação aceito pelo negócio, que dimensiona o volume (PEND-02).
- Alerta intermediário (por exemplo em 50%) para dar tempo de agir antes do teto.

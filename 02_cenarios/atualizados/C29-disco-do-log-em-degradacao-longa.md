# C29 — Disco do log esgota durante uma degradação longa (antes R08)

**Status:** ✅ Atualizado (decidido pelo Bruno em 13/10/2026) · **ADRs:** [ADR-055](../../03_adr/ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md), [ADR-041](../../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md), [ADR-019](../../03_adr/ADR-019-wal-proprio-volume-dedicado.md), [ADR-018](../../03_adr/ADR-018-dois-logs-raft-e-replicacao.md), [ADR-014](../../03_adr/ADR-014-modo-degradado-opcao-b.md), [ADR-044](../../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [ADR-045](../../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md), [ADR-047](../../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md) · **Relacionados:** [C32](C32-particao-e-lease-do-modo-degradado.md), [C33](C33-reconstrucao-em-tabela-sombra.md), [C12](C12-modo-degradado.md), [C21](C21-disco-fsync-e-reconstrucao.md), [X02](../complexos/X02-modo-degradado.md)

## Cenário

No modo degradado, a AWS retém todas as entradas desde `fromIndex` para a Azure fazer catch-up. A 3.000 TPS e ~2 KB por entrada são **~0,5 TB por dia por shard**. Se os votantes ficarem sem disco, o fsync falha e **não há mais commit**: o modo criado para manter o sistema de pé o derrubaria.

## Decisão

A retenção para a Azure tem **teto de 70% do volume**. Passado o teto, a Azure vira "fora da janela" e volta por **reconstrução completa**. **Sem descarga para S3 na v1.**

**Desde o ADR-055 (13/10):** o log de replicação vive no **Pebble, em volume próprio** por nó, separado do volume pequeno do log do Raft. O teto de 70% vale para o volume do Pebble e mede o **espaço lógico retido**, como antes (resposta 3b do Bruno). O log do Raft continua curto: ele é truncado até o checkpoint do Pebble, e não até o ACK da Azure.

Com o lease do modo degradado ([ADR-044](../../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)), a degradação sem prorrogação dura no máximo D, e **D passa a ser o parâmetro de dimensionamento**: o volume deve reter D de escrita à taxa de pico abaixo dos 70%. O dimensionamento só vale para instalações `DEGRADED_POLICY=accept`; com `reject` o log não cresce por degradação ([ADR-045](../../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)).

## Diagrama: teto de retenção

```mermaid
flowchart TD
    A["Modo degradado até o fim de D, ou mais se prorrogado"] --> B["Log de replicação cresce no Pebble, volume próprio"]
    A --> R["Log do Raft segue curto: truncado até o checkpoint do Pebble"]
    B --> C{"Espaço lógico retido acima de 70 por cento?"}
    C -->|"não"| D["Segue, Azure continua elegível ao catch-up"]
    C -->|"sim"| E["Azure marcada como fora da janela, DeleteRange da faixa antiga"]
    E --> G["Espaço físico só volta depois da compactação do Pebble"]
    G --> F["Reconstrução completa da Azure quando voltar: drctl rebuild"]
    B -.->|"risco 30: físico acima do lógico"| H["Volume do Pebble cheio: checkpoint falha, Raft para de truncar"]
```

## Etapas

1. **Alertas** de ocupação por votante com **projeção de tempo até encher**, e dimensionamento do volume para D, a maior degradação aceita (PEND-02), mais as prorrogações previstas. Uma verificação na partida ou um alerta compara D com o tempo de retenção estimado a 70% (proposta, a definir): se a retenção for menor que D, a Azure perde a elegibilidade **antes** de o lease acabar.
2. Com 70% de um volume de, por exemplo, 2 TB, a retenção para a Azure cobre pouco menos de 3 dias a 3.000 TPS. O número real depende do tamanho médio da entrada, medido em teste.
3. O log do Raft é curto e **independe** desse crescimento; só o log de replicação cresce ([ADR-018](../../03_adr/ADR-018-dois-logs-raft-e-replicacao.md), [ADR-055](../../03_adr/ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)). Ele fica no seu volume pequeno e é truncado até o `DurableIndex` do Pebble (checkpoint com sync), mais uma folga limitada por `RAFT_RETAIN_MAX`.
4. Passado o teto, a faixa antiga é apagada com `DeleteRange` e a Azure só volta por `drctl rebuild`. O espaço físico só é devolvido quando a compactação do Pebble passa pela faixa.

## Falhas e tratativas

- **Janela perdida e a AWS cai.** Depois do teto, a Azure não alcança a AWS pelo log. Se a AWS cair nesse período, a promoção da Azure perde tudo o que foi escrito desde `fromIndex` ([R13](../a_rever/R13-escritas-orfas-reconciliacao.md)). A perda tem um teto de tempo, D, enquanto não houver prorrogação; cada `RENEW` humano estende esse teto e também a necessidade de disco.
- **Custo e duração da reconstrução completa.** A reconstrução da Azure segue o protocolo da tabela sombra ([C33](C33-reconstrucao-em-tabela-sombra.md)): copiar todo o conteúdo para o Cosmos consome RU e leva um tempo ainda não medido, durante o qual o log precisa estar retido desde F ([R09](../a_rever/R09-resincronizacao-lenta-ou-bloqueada.md), [R18](../a_rever/R18-rto-nao-quantificado.md)).
- **Espaço físico acima do lógico (risco 30, registrado; decisão 3b não reaberta).** O teto mede o espaço lógico retido. O Pebble só libera o disco de um `DeleteRange` depois da compactação, que também precisa de espaço livre temporário. Na degradação longa o físico pode passar do lógico, e os três votantes enchem quase juntos porque retêm a mesma faixa. Com o volume do Pebble cheio, o checkpoint com sync falha (fatal, [ADR-055](../../03_adr/ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)) e o log do Raft deixa de ser truncado. Padrão: métrica e alerta da ocupação física, sem mudar o critério do teto. A alternativa 3a (medir o real e forçar compactação acima de 60%) fica registrada no ADR-055.
- **Disco do learner.** O mesmo risco vale do lado Azure quando o apply atrasa ([R03](../a_rever/R03-backlog-de-apply-no-cosmos.md)).
- **S3 descartado na v1.** O catch-up lendo do S3 atravessaria o link, exigiria credencial AWS na Azure ([R23](../a_rever/R23-hipoteses-gerais.md), H10) e pioraria o desastre em que a AWS some.

## Pontos em aberto

- Tempo máximo de degradação aceito pelo negócio (D), que dimensiona o volume (PEND-02).
- Alerta intermediário (por exemplo em 50%) para dar tempo de agir antes do teto.
- Tamanho do volume do Pebble com folga para a amplificação de espaço e para a compactação (PEND-22), medido no spike S1 com uma degradação simulada.

# ADR-041 — Disco do log em degradação longa: teto de 70% e reconstrução completa; sem S3 na v1

**Status:** Decidido (Bruno) · **Data:** 13/10/2026 · **Complementado em 13/10/2026 pelo [ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)** (teto mantido como está, resposta 3b)

## Contexto

No modo degradado a AWS retém todas as entradas desde `fromIndex` para a Azure fazer catch-up. A 3.000 TPS e cerca de 2 KB por entrada são ~0,5 TB por dia por shard. Sem disco, o fsync falha e não há mais commit.

## Decisão

1. A retenção para a Azure tem **teto de 70% do volume**. Passado o teto, a Azure vira "fora da janela" e voltará por **reconstrução completa** (`drctl rebuild`).
2. **Não há descarga para S3 na v1.**
3. Alerta de ocupação por votante com projeção de tempo até encher.
4. **Desde o ADR-055 (resposta 3b do Bruno):** o log de replicação é um log segmentado, num volume só dele. O teto vale para **esse volume** e continua medido como está: pelo **espaço lógico retido** (do primeiro índice retido até a cabeça). Como o truncamento apaga segmentos inteiros, o espaço físico só passa do lógico pelo segmento parcialmente necessário.

## Alternativas consideradas

- **Descarregar segmentos antigos para o S3:** mantém a Azure elegível por mais tempo, mas o catch-up passa a ler do S3 pelo link, exige credencial AWS na Azure e piora o desastre em que a AWS some.

## Consequências

- Prefere-se perder a elegibilidade da Azure a derrubar o disco da AWS.
- Passado o teto, a Azure não tem como alcançar a AWS pelo log; se a AWS cair nessa janela, a perda é tudo o que foi escrito desde `fromIndex` (órfãs, [R13](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md)). Por isso o tempo máximo de degradação aceito pelo negócio (PEND-02) dimensiona o volume.
- **Risco 30 encerrado (13/10).** Com o Pebble, o espaço físico podia ficar bem acima do lógico até a compactação. Com o log segmentado ([ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)), apagar o começo remove arquivos e o disco volta na hora.
- O custo e o tempo da reconstrução completa não têm número ([R09](../02_cenarios/a_rever/R09-resincronizacao-lenta-ou-bloqueada.md), [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md)).

## Onde está o detalhe

- [ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)

- [C29-disco-do-log-em-degradacao-longa.md](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)
- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

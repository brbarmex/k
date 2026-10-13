# ADR-041 — Disco do log em degradação longa: teto de 70% e reconstrução completa; sem S3 na v1

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

No modo degradado a AWS retém todas as entradas desde `fromIndex` para a Azure fazer catch-up. A 3.000 TPS e cerca de 2 KB por entrada são ~0,5 TB por dia por shard. Sem disco, o fsync falha e não há mais commit.

## Decisão

1. A retenção para a Azure tem **teto de 70% do volume**. Passado o teto, a Azure vira "fora da janela" e voltará por **reconstrução completa** (`drctl rebuild`).
2. **Não há descarga para S3 na v1.**
3. Alerta de ocupação por votante com projeção de tempo até encher.

## Alternativas consideradas

- **Descarregar segmentos antigos para o S3:** mantém a Azure elegível por mais tempo, mas o catch-up passa a ler do S3 pelo link, exige credencial AWS na Azure e piora o desastre em que a AWS some.

## Consequências

- Prefere-se perder a elegibilidade da Azure a derrubar o disco da AWS.
- Passado o teto, a Azure não tem como alcançar a AWS pelo log; se a AWS cair nessa janela, a perda é tudo o que foi escrito desde `fromIndex` (órfãs, [R13](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md)). Por isso o tempo máximo de degradação aceito pelo negócio (PEND-02) dimensiona o volume.
- O custo e o tempo da reconstrução completa não têm número ([R09](../02_cenarios/a_rever/R09-resincronizacao-lenta-ou-bloqueada.md), [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md)).

## Onde está o detalhe

- [C29-disco-do-log-em-degradacao-longa.md](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)
- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

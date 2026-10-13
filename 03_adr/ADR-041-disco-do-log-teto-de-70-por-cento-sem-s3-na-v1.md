# ADR-041 — Disco do log em degradação longa: teto de 70% e reconstrução completa; sem S3 na v1

**Status:** Decidido (Bruno) · **Data:** 13/10/2026 · **Complementado em 13/10/2026 pelo [ADR-055](ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)** (teto mantido como está, resposta 3b)

## Contexto

No modo degradado a AWS retém todas as entradas desde `fromIndex` para a Azure fazer catch-up. A 3.000 TPS e cerca de 2 KB por entrada são ~0,5 TB por dia por shard. Sem disco, o fsync falha e não há mais commit.

## Decisão

1. A retenção para a Azure tem **teto de 70% do volume**. Passado o teto, a Azure vira "fora da janela" e voltará por **reconstrução completa** (`drctl rebuild`).
2. **Não há descarga para S3 na v1.**
3. Alerta de ocupação por votante com projeção de tempo até encher.
4. **Desde o ADR-055 (resposta 3b do Bruno):** o log de replicação vive no Pebble, num volume só dele. O teto vale para **esse volume** e continua medido como está: pelo **espaço lógico retido** (do primeiro índice retido até a cabeça). Não se força compactação.

## Alternativas consideradas

- **Descarregar segmentos antigos para o S3:** mantém a Azure elegível por mais tempo, mas o catch-up passa a ler do S3 pelo link, exige credencial AWS na Azure e piora o desastre em que a AWS some.

## Consequências

- Prefere-se perder a elegibilidade da Azure a derrubar o disco da AWS.
- Passado o teto, a Azure não tem como alcançar a AWS pelo log; se a AWS cair nessa janela, a perda é tudo o que foi escrito desde `fromIndex` (órfãs, [R13](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md)). Por isso o tempo máximo de degradação aceito pelo negócio (PEND-02) dimensiona o volume.
- **Risco registrado (risco 30, [02_riscos](../06_pendencias_e_riscos/02_riscos.md)):** o Pebble só devolve o espaço de um `DeleteRange` quando a compactação passa por aquela faixa, e a própria compactação precisa de espaço livre temporário. O espaço **físico** ocupado pode ficar acima do lógico que o teto mede, justamente na degradação longa, quando o disco está apertado. Como os três votantes retêm a mesma faixa, eles enchem quase ao mesmo tempo. Um volume do Pebble cheio não para o fsync do Raft diretamente, mas impede o checkpoint do Pebble, e com isso o log do Raft deixa de ser truncado e passa a crescer no volume pequeno dele. A decisão não foi reaberta: o padrão é expor a ocupação física como métrica e alertar sobre ela, sem mudar o critério do teto. A alternativa 3a (medir o espaço real e forçar compactação acima de 60%) fica registrada.
- O custo e o tempo da reconstrução completa não têm número ([R09](../02_cenarios/a_rever/R09-resincronizacao-lenta-ou-bloqueada.md), [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md)).

## Onde está o detalhe

- [ADR-055](ADR-055-log-de-replicacao-em-pebble-derivado-do-raft.md)

- [C29-disco-do-log-em-degradacao-longa.md](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)
- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

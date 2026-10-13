# ADR-053 — O modo Eventual é só de leitura; o apply paralelo sai da v1

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026) · **Data:** 13/10/2026

## Contexto

O [ADR-023](ADR-023-modos-de-escrita-e-leitura.md) permitia que, no Eventual, o apply na célula da líder corresse em paralelo com a espera da Azure. Um leitor podia ver uma escrita **ainda não durável na Azure**, que nunca seria ACKada se a AWS caísse antes. Quem leu agiu sobre um valor que depois "não aconteceu". O ganho era esconder `t_apply` (alguns ms) atrás da espera da Azure. O [ADR-009](ADR-009-eventual-e1.md) já dizia que só a leitura muda; os dois ADRs se contradiziam.

## Decisão

1. **O apply é sempre serial e depois do gate**, para todas as instalações.
2. A variável `WRITE_MODE` **sai da v1**. O Eventual (E1) permanece como **modo de leitura** (`READ_MODE_DEFAULT`, `ConsistentRead`).
3. Reavaliar o apply paralelo, como opt-in por instalação com contrato "pode ler escrita não confirmada", só se a medição (spike S5) mostrar que `t_apply` estoura o orçamento.

## Alternativas consideradas

- Manter o apply paralelo opt-in desde já: risco desproporcional ao ganho de alguns ms.

## Consequências

- O ACK inclui `t_apply` em todos os casos; o SLO publicado como overhead + banco ([ADR-036](ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)) já absorve isso.
- `applied` nunca passa do gate: sustenta a barreira do [ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md).
- O cenário C04 passa a descrever o modo Eventual de leitura, sem escrita paralela.

## Onde está o detalhe

- [C04-escrita-eventual.md](../02_cenarios/atualizados/C04-escrita-eventual.md)
- [06_modos_de_escrita_e_leitura_e_latencia.md](../01_arquitetura/06_modos_de_escrita_e_leitura_e_latencia.md)

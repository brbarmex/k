# ADR-023 — Modos de escrita (Strong serial, Eventual paralelo) e de leitura (padrão forte)

**Status:** Decidido (Bruno); parte de escrita substituída pelo ADR-053 em 13/10/2026 · **Data:** 12/10/2026

## Contexto

O custo de latência do apply depois da Azure versus a janela de visibilidade antes da durabilidade.

## Decisão

`WRITE_MODE` (obrigatória): Strong aplica em série depois do gate; Eventual aplica em paralelo, mas o ACK só sai depois da Azure. `READ_MODE_DEFAULT` (obrigatória, padrão `strong`): `ConsistentRead=true` é sempre forte; ausente ou `false` segue o padrão. O modo deve ser igual nas células do grupo (divergência alerta).

## Alternativas consideradas

Modo por tabela; leitura forte só por `ConsistentRead`.

## Consequências

No Eventual o leitor pode ver o dado poucos ms antes da durabilidade na Azure (aceito).

## Atualização de 13/10/2026 (revisão crítica)

O **apply paralelo do modo Eventual saiu da v1** e a variável `WRITE_MODE` foi removida ([ADR-053](ADR-053-eventual-sem-apply-paralelo-na-v1.md)). Permanecem a leitura (`READ_MODE_DEFAULT`, padrão `strong`, `ConsistentRead`) e a barreira `min(ReadIndex, gate)` ([ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md)).

## Onde está o detalhe

- [06_modos_de_escrita_e_leitura_e_latencia.md](../01_arquitetura/06_modos_de_escrita_e_leitura_e_latencia.md)

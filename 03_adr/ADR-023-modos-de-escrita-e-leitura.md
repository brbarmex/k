# ADR-023 — Modos de escrita (Strong serial, Eventual paralelo) e de leitura (padrão forte)

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

O custo de latência do apply depois da Azure versus a janela de visibilidade antes da durabilidade.

## Decisão

`WRITE_MODE` (obrigatória): Strong aplica em série depois do gate; Eventual aplica em paralelo, mas o ACK só sai depois da Azure. `READ_MODE_DEFAULT` (obrigatória, padrão `strong`): `ConsistentRead=true` é sempre forte; ausente ou `false` segue o padrão. O modo deve ser igual nas células do grupo (divergência alerta).

## Alternativas consideradas

Modo por tabela; leitura forte só por `ConsistentRead`.

## Consequências

No Eventual o leitor pode ver o dado poucos ms antes da durabilidade na Azure (aceito).

## Onde está o detalhe

- [06_modos_de_escrita_e_leitura_e_latencia.md](../01_arquitetura/06_modos_de_escrita_e_leitura_e_latencia.md)

# ADR-012 — Sem downgrade automático de Strong para Eventual

**Status:** Confirmado (requisito) · **Data:** 10/10/2026

## Contexto

Uma degradação de consistência silenciosa quebraria aplicações que dependem de leitura forte.

## Decisão

Nenhum componente rebaixa uma leitura Strong para Eventual. Ver a regra de `READ_MODE_DEFAULT` (ADR-023).

## Alternativas consideradas

Fallback automático para Eventual: rejeitado.

## Consequências

Quando a leitura forte não é possível, a resposta é um erro recuperável.

## Onde está o detalhe

- [06_modos_de_escrita_e_leitura_e_latencia.md](../01_arquitetura/06_modos_de_escrita_e_leitura_e_latencia.md)

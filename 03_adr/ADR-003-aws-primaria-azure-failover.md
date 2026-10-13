# ADR-003 — AWS primária e Azure como failover, ambas só em São Paulo

**Status:** Confirmado (restrição dura) · **Data:** 11/10/2026

## Contexto

O negócio permite apenas AWS e Azure, nas regiões de São Paulo.

## Decisão

AWS (sa-east-1) é a primária com DynamoDB; Azure (Brazil South) é o failover com Cosmos DB, por um Cloud-Adapter. Sem terceira nuvem, datacenter próprio ou outra região.

## Alternativas consideradas

Terceiro domínio ou outra região (ADR-008): descartado pela restrição.

## Consequências

O desenho depende do link dedicado entre as duas nuvens. A perda simultânea das duas nuvens é risco aceito.

## Onde está o detalhe

- [01_forma_do_app_e_escopo.md](../01_arquitetura/01_forma_do_app_e_escopo.md)

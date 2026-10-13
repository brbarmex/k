# ADR-001 — Camada intermediária agnóstica a cloud e a banco

**Status:** Confirmado (requisito) · **Data:** 10/10/2026

## Contexto

Aplicações precisam de replicação entre nuvens sem acoplar cada uma a um provedor ou a um banco.

## Decisão

Uma camada entre a aplicação e o banco, que expõe a API do banco primário e mantém capacidades por backend.

## Alternativas consideradas

Replicação nativa de cada banco (Global Tables, Cosmos multi-região): não atende a restrição de só AWS e Azure em São Paulo com RPO 0 coordenado.

## Consequências

Mais um componente no caminho crítico, com o orçamento de latência de 50 ms. A API canônica é a do DynamoDB.

## Onde está o detalhe

- [01_forma_do_app_e_escopo.md](../01_arquitetura/01_forma_do_app_e_escopo.md)

# ADR-024 — DELETE real com ordem por chave; TTL local; GSI

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

Clientes usam TTL (alguns), Scan (poucos) e Query em GSI (algumas).

## Decisão

DELETE real (sem tombstone), com ordem por chave no applier e delete condicional por versão. A expiração por TTL é local a cada célula e não é replicada. Leitura forte em GSI vale até a propagação do índice. Scan não tem snapshot entre páginas.

## Alternativas consideradas

Tombstone com TTL.

## Consequências

Risco de "zumbi" por apply paralelo e replay; leituras só abrem depois do high-water mark na partida.

## Onde está o detalhe

- [05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md](../01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md)
- [X06-delete-ttl-gsi.md](../02_cenarios/complexos/X06-delete-ttl-gsi.md)

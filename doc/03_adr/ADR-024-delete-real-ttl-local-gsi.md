# ADR-024 — DELETE real com ordem por chave; TTL local; GSI

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

Clientes usam TTL (alguns), Scan (poucos) e Query em GSI (algumas).

## Decisão

DELETE real (sem tombstone), com ordem por chave no applier e delete condicional por versão. A expiração por TTL é local a cada célula e não é replicada. Query em GSI é recusada ([ADR-050](ADR-050-query-em-gsi-recusada.md)). Scan não tem snapshot entre páginas.

## Alternativas consideradas

Tombstone com TTL.

## Consequências

Risco de "zumbi" no replay (o apply é sempre depois do gate, [ADR-057](ADR-057-um-unico-modo-strong-consistency.md), e a mesma chave é aplicada na ordem do log, [ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)); leituras só abrem depois do high-water mark na partida.

## Atualização de 13/10/2026 (revisão crítica)

- O DELETE real fica com uma **invariante formal** e um protocolo de reconstrução em tabela sombra ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).
- **Query em GSI é recusada** (ampliado em 13/10: modo único strong); a frase "leitura forte em GSI até a propagação do índice" não vale mais ([ADR-050](ADR-050-query-em-gsi-recusada.md)).

## Onde está o detalhe

- [05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md](../01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md)
- [X06-delete-ttl-gsi.md](../02_cenarios/complexos/X06-delete-ttl-gsi.md)

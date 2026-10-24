# ADR-033 — Promoção da Azure sempre humana, só depois de D + M

**Status:** **Substituído pelo ADR-064** (24/10/2026); era Decidido o desenho · **Data:** 11/10/2026

## Contexto

Da Azure, "AWS morta" e "link caído" parecem iguais; promoção automática pode causar split-brain.

## Decisão

A promoção `ForcePromote` (nova epoch) é feita por pessoa, com checklist de evidências. O desenho com lease está no ADR-044.

## Alternativas consideradas

Promoção automática.

## Consequências

O RTO inclui o tempo de decisão humana (hipótese H6).

## Atualização de 13/10/2026 (revisão crítica)

A promoção continua **sempre humana**, mas agora o software a **recusa antes de D + M** contados desde o último contato recebido da AWS ([ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)). Com `DEGRADED_POLICY=reject` vale o fencing por maioria Azure na epoch nova. O Bruno aprovou esse desenho na revisão crítica; a PEND-01 fecha quando D e M forem aprovados (PEND-02) e o modelo TLA+ provar a regra.

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

Substituído por [ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md): a promoção é um **comando via API, por automação ou operador**, sem esperar D + M e sem cercar a AWS antes. Risco aceito (Lb): com a AWS viva, o comando gera duas líderes e uma história é descartada. O argumento original deste ADR (da Azure, "AWS morta" e "link caído" parecem iguais) continua verdadeiro e é a origem desse risco.

**Na POC:** não existe promoção.

## Onde está o detalhe

- [X03-desastre-e-promocao-da-azure.md](../02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md)

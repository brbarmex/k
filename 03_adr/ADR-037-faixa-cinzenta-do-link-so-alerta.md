# ADR-037 — Link lento na faixa cinzenta: só alertar, RPO continua 0

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

O modo degradado só entra quando o gate espera a Azure por mais de `DEGRADE_AFTER` (2 s). Entre a meta (perto de 50 ms de overhead) e 2 s, o link está lento mas entrega: nada dispara.

## Decisão

Na faixa cinzenta o sistema **segue em modo normal, com RPO = 0, e apenas alerta** (P99 do gate por cloud e por learner). O modo degradado entra somente pelo limite de `DEGRADE_AFTER`.

## Alternativas consideradas

- **Degradar automaticamente por limite de latência:** troca segurança do dado por latência e só faz sentido depois do aceite formal do RPO > 0 (PEND-02).

## Consequências

- A latência de escrita pode ficar acima da meta, até 2 s, sem ação automática. O P99 de 150 ms do cliente pode ser violado nessa faixa.
- É preciso limitar as requisições em voo e rejeitar rápido quando o limite é atingido, para a lentidão não virar fila e memória sem fim (a definir no spike S1).
- O tráfego de replicação não pode competir com o encaminhamento entre clouds: QoS ou banda reservada.
- Reavaliar depois do PEND-02 e das medições reais.

## Onde está o detalhe

- [C25-link-lento-faixa-cinzenta.md](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md)
- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

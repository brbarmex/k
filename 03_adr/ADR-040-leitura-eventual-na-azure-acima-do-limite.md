# ADR-040 — Leitura eventual na Azure acima do limite de defasagem: redirecionar à AWS ou recusar

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

No modo degradado a Azure deixa de receber entradas. Uma leitura **eventual** servida por uma célula Azure pode devolver dado com horas ou dias de atraso, sem o cliente saber. O padrão de leitura é forte ([ADR-023](ADR-023-modos-de-escrita-e-leitura.md)); o caso só existe quando o cliente pede leitura eventual.

## Decisão

1. A defasagem é medida **com relógio monotônico local**: `max(idade do último heartbeat da líder, tempo estimado para aplicar o que já chegou)`. Defasagem desconhecida (link caído) conta como acima do limite.
2. Dentro de `MAX_STALENESS`: lê o Cosmos e devolve `x-dr-observed-index` e `x-dr-staleness-ms`.
3. Acima do limite: **redireciona a leitura à AWS se link e líder estiverem alcançáveis** (limite de 3 s); senão **recusa** com `503`, `retryAfter` e indicação de dado velho.

## Alternativas consideradas

- **Sempre recusar:** mais simples, mas recusa leituras que a AWS poderia atender.

## Consequências

- Com o link caído (o caso típico da degradação) não há como redirecionar, então a leitura eventual na Azure falha em vez de devolver dado velho. É intencional.
- O cabeçalho informa, não impede: o dado que nunca pode ser velho deve usar leitura forte.
- Valor inicial de `MAX_STALENESS`: 5 s (sugestão), a ajustar depois do spike.

## Onde está o detalhe

- [C28-leitura-eventual-na-azure-defasada.md](../02_cenarios/atualizados/C28-leitura-eventual-na-azure-defasada.md)
- [C05-leitura-strong-e-eventual.md](../02_cenarios/atualizados/C05-leitura-strong-e-eventual.md)

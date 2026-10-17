# ADR-047 — DELETE real com invariante formal; reconstrução em tabela sombra

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026) · **Data:** 13/10/2026

## Contexto

O DELETE real sem tombstone ([ADR-024](ADR-024-delete-real-ttl-local-gsi.md)) é correto na reaplicação em ordem por chave a partir do progresso do applier: `Put(10)` recria a chave e `Delete(11)` a remove de novo. O estado errado existe só durante o replay, e a regra "leituras só abrem depois do high-water mark" o esconde. Faltavam a prova e um protocolo para os caminhos que **não passam pelo applier em ordem** (reconstrução, restauração PITR, cópia de outra célula).

## Decisão

1. **Manter o DELETE real**, sem tombstone na v1.
2. **Invariante `I-DEL`:** com as leituras abertas, o estado materializado da célula é a dobra do log até `applied`. A regra de abrir leituras só depois do high-water mark é imposta **por código** e coberta por teste de propriedade (sequências aleatórias de Put e Delete, com queda em qualquer ponto, convergem para o mesmo estado) e pelo modelo TLA+.
3. **Protocolo de reconstrução em tabela sombra** (cópia difusa mais replay):
   1. registrar o índice **F** do log antes de começar e reter o log desde F;
   2. criar a tabela sombra (sem GSIs, ADR-050) e copiá-la por varredura, gravando cada item com a condição de versão;
   3. reaplicar o log a partir de F até a cabeça (a cópia converge porque o apply é condicional por versão e o DELETE é reaplicado em ordem);
   4. com o lag abaixo do limite, pausar a escrita da célula, fechar o último trecho e fazer o **cutover** do mapeamento lógico para a tabela sombra;
   5. verificar por **checksum por faixa de chaves** (anti-entropia) antes de religar as leituras.
4. **TTL local:** um replay pode recriar um item já expirado até o TTL local apagá-lo de novo. O comportamento é igual ao do DynamoDB, onde o item expirado pode ser lido até a remoção, e é documentado para os clientes.
5. **Tombstone:** fora da v1. Se os testes mostrarem zumbi, a alternativa registrada é uma **tabela separada de tombstones consultada só em replay e rebuild**, com uma escrita extra por DELETE. Tombstone dentro do item foi descartado: obriga a filtrar `Query`, `Scan` e GSI e muda o conteúdo das páginas com `Limit`.

## Alternativas consideradas

- Tombstone no item, filtrado nas leituras (custo na leitura e quebra de transparência).
- Tabela de tombstones sempre consultada (custo em toda escrita).

## Consequências

- O nome lógico passa a ter **geração** ([ADR-022](ADR-022-banco-por-celula-nome-logico.md)): o cutover troca a tabela física sem mudar o nome visto pelo cliente. A allowlist e o IAM precisam admitir a tabela sombra.
- O log precisa estar retido desde F. Se a varredura demorar mais que a retenção do teto de 70% ([ADR-041](ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)), o rebuild falha e recomeça.
- Custo de WCU/RU durante a varredura, no DynamoDB e no Cosmos. O Cosmos precisa do mesmo protocolo pelo Cloud-Adapter.
- A anti-entropia por checksum detecta divergência, não a previne.

## Onde está o detalhe

- [C33-reconstrucao-em-tabela-sombra.md](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md)
- [X06-delete-ttl-gsi.md](../02_cenarios/complexos/X06-delete-ttl-gsi.md)

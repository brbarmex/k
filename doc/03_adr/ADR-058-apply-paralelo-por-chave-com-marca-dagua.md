# ADR-058 — Apply paralelo por chave, com a marca d'água contígua como `applied`

**Status:** Decidido (Bruno, 14/10/2026: resposta 1a) · **Data:** 14/10/2026 · **Esclarece:** [ADR-057](ADR-057-um-unico-modo-strong-consistency.md) (o "serial" do apply); **alinha o código com:** [ADR-035](ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md), [X06](../02_cenarios/complexos/X06-delete-ttl-gsi.md) (ordem por chave)

## Contexto

O ADR-057 escreveu que o applier aplica "em série e só depois do gate". A intenção era temporal: **nada é aplicado antes do gate**, ao contrário do antigo apply paralelo do modo Eventual, que aplicava enquanto a Azure ainda não tinha confirmado. O esboço (`usecase/apply.go`) e a spec da POC (AP-03) leram "em série" como **um item por vez**: o applier manda um `PutItem`, espera a resposta e só então manda o próximo.

A conta não fecha com a meta de 3.000 TPS:

- Um `PutItem` condicional no DynamoDB leva de 4 a 8 ms (estimativa; o spike S5 mede). Um item por vez dá de **125 a 250 itens/s por shard**.
- Com 8 shards (o exemplo da análise do [ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)), chegam **375 itens/s por shard**. A fila do applier cresce sem limite.
- Na líder o ACK espera o apply local ([C03](../02_cenarios/atualizados/C03-escrita-strong.md), 6.9 item 4), então essa fila entra direto na latência do ACK. Nas followers, a leitura forte espera o `applied` alcançar a barreira e passa a dar `READ_TIMEOUT`.
- Um `BatchWriteItem` do DynamoDB não resolve: ele **não aceita condição**, e a checagem `_dr_v < :v` que torna o replay idempotente se perderia. O `TransactWriteItems` aceita condição, mas custa o dobro de WCU e falha inteiro se um item já estiver aplicado, o que é o caso normal do replay.

O próprio pacote já previa o desenho certo: o ADR-035 aplica os itens de um `BatchWriteItem` "em paralelo limitado", e o X06 diz "operações da mesma chave rodam uma de cada vez, na ordem do log; chaves diferentes seguem em paralelo".

Medição no código da POC, com banco simulado de 4 ms por item e chaves distintas (`BenchmarkApplierThroughput`): 1 worker, 229 itens/s; 4 workers, 899; 16 workers, 3.600; 64 workers, 11.422.

## Decisão

1. **"Serial" no ADR-057 quer dizer "depois do gate".** O despacho de uma entrada espera `gate >= índice`; nenhum item é gravado antes disso. A invariante `applied <= gate` continua.
2. **Paralelo por chave.** Cada shard tem `APPLY_WORKERS` workers (padrão **16**, de 1 a 256). Um despachante lê as entradas em ordem, espera o gate e entrega cada item ao worker `hash(tabela lógica, chave canônica) mod APPLY_WORKERS` (FNV-1a). Cada worker grava um item por vez, na ordem em que recebeu. Resultado:
   - a **mesma chave** cai sempre no mesmo worker e é gravada **na ordem do log**, o que o DELETE real exige ([ADR-024](ADR-024-delete-real-ttl-local-gsi.md), [ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md));
   - **chaves diferentes** não esperam umas pelas outras, inclusive os itens de um mesmo `BatchWriteItem` ([ADR-035](ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md)).
3. **`applied` é a marca d'água contígua:** o maior índice que, com todos os anteriores, está no banco. Itens de índices maiores podem já estar gravados. O ACK da líder, a barreira da leitura forte ([ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md)) e o `/status` usam só a marca d'água.
4. **Progresso salvo = marca d'água** (`índice`, `ordem` = número de itens da entrada), gravado sem fsync a cada avanço ([ADR-048](ADR-048-skip-por-item.md), [ADR-055](ADR-055-log-de-replicacao-segmentado-derivado-do-raft.md)). Na partida, o replay reaplica tudo o que vem depois dela; itens que já estavam no banco recebem `ConditionalCheckFailed` e contam como "já aplicado".
5. **Item venenoso ([ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md)):** o primeiro erro permanente estaciona o applier do shard naquela célula. O despacho para; itens **anteriores** ao venenoso, em outros workers, terminam; itens **posteriores** são descartados, salvo os que já estavam a caminho do banco. A marca d'água para logo antes do item venenoso. Com dois itens venenosos, vale o primeiro na ordem do log. A saída continua humana.
6. **Pool de conexões do SDK** dimensionado pelos workers: `max(2 × APPLY_WORKERS, 32)` conexões ociosas por host. O padrão do aws-sdk-go-v2 guarda só 10, e cada gravação acima disso abriria conexão nova (TLS completo na AWS real).
7. **Observabilidade:** `dr_apply_item_duration_seconds` (histograma por item, com as repetições), `dr_apply_workers` e `dr_apply_inflight` (itens a caminho do banco); `/status` com `apply.workers` e `apply.emVoo`. `APPLY_WORKERS=1` reproduz o comportamento antigo, para comparação.

## Alternativas consideradas

- **Um item por vez e mais shards:** cada shard a mais é um WAL do etcd, um fsync e mais IOPS ([ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md), risco 36), e um grupo Raft a mais para operar. Resolve a vazão do apply pelo lado mais caro.
- **`BatchWriteItem` no apply:** sem condição, perde o replay idempotente. Descartado.
- **`TransactWriteItems` no apply:** condição por item, mas o dobro de WCU, limite de 100 itens, e falha inteira quando um item já foi aplicado (replay). Descartado.
- **Paralelo sem ordem por chave**, contando só com a condição de versão: correto para `PutItem`, mas um `Put` antigo depois de um `Delete` mais novo recria o item (zumbi), porque `attribute_not_exists(_dr_v)` passa a ser verdadeiro. Descartado.
- **ACK da líder esperando só o próprio item**, e não a marca d'água: tiraria do ACK a espera por chaves lentas de índices menores, sem mudar a leitura forte (que já espera a marca d'água). Registrado para avaliar com a medição do S5; não adotado na v1, para manter uma única regra de "aplicado".

## Consequências

- **Vazão do apply por shard ≈ `APPLY_WORKERS / t_put`**, enquanto as chaves se espalham. Com 16 workers e 4 a 8 ms por item, de 2.000 a 4.000 itens/s por shard, folga de 5 a 10 vezes sobre os 375/s do exemplo de 8 shards.
- **Chave quente continua limitada a `1 / t_put`** (de 125 a 250 escritas/s na mesma chave), por desenho: a ordem por chave exige um item por vez. Além disso, o worker da chave quente atrasa as outras chaves que caem nele (bloqueio na cabeça da fila, 1/`APPLY_WORKERS` das chaves), e a marca d'água contígua atrasa o ACK de todo o shard enquanto o atraso durar. Aparece em `dr_apply_inflight` alto com `dr_applied_index` parado. O DynamoDB já limita uma partição a 1.000 WCU/s, então a chave quente é um limite do banco de qualquer forma ([R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md)).
- **Leitura forte:** pode devolver um valor de índice acima da barreira (gravado antes da marca d'água chegar). Isso continua linearizável: o item gravado já está commitado e abaixo do gate, porque nada é despachado antes do gate.
- **`I-DEL` ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md))** passa a valer **por chave**: o estado de cada chave é a dobra das operações dela até algum índice entre a marca d'água e o gate. O zumbi no replay continua escondido pela regra de só abrir leituras depois do high-water mark.
- **Entrada venenosa:** com workers em paralelo, itens posteriores de outras chaves que já estavam a caminho do banco podem estar gravados além do item venenoso. A leitura via `DataReplication` não os enxerga como "aplicados", porque a marca d'água para antes; uma leitura direta no banco pode vê-los. O replay depois do SKIP os reaplica sem efeito.
- **Conexões:** `APPLY_WORKERS` conexões por shard e por célula ao banco (16 × 8 shards = 128 por célula no exemplo). O Cosmos, pelo Cloud-Adapter, recebe o mesmo paralelismo e consome RU por partição da mesma forma.
- **Custo:** o número de gravações não muda (mesmas WCU/RU); só a concorrência.
- **Spike S5:** medir `t_apply` e a vazão por shard com 1, 4, 16 e 64 workers no DynamoDB real, com e sem chave quente, e fixar o padrão de `APPLY_WORKERS`.

## Código

- `DataReplicationApp/internal/usecase/apply.go` (despachante, workers por chave, marca d'água, estacionamento), com testes de paralelismo, ordem por chave, marca d'água, item venenoso e o benchmark.
- `DataReplicationApp/internal/adapter/out/dynamodb/store.go` (pool HTTP), `internal/platform/config` (`APPLY_WORKERS`).
- Spec da POC: AP-03, AP-06, AP-07 e AP-11 em [SPEC_POC_LOCAL.md](../../spec/SPEC_POC_LOCAL.md).

## Onde está o detalhe

- [06_leitura_forte_escrita_serial_e_latencia.md](../01_arquitetura/06_leitura_forte_escrita_serial_e_latencia.md) (7.1)
- [C03-escrita-strong.md](../02_cenarios/atualizados/C03-escrita-strong.md), [C24-orcamento-de-50ms-e-escrita-no-dynamodb.md](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md), [C26-entrada-venenosa.md](../02_cenarios/atualizados/C26-entrada-venenosa.md)
- [X06-delete-ttl-gsi.md](../02_cenarios/complexos/X06-delete-ttl-gsi.md)

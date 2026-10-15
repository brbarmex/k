# ADR-061 — O ACK e o GetItem esperam a marca d'água do slot da chave, não a marca d'água global

**Status:** Descartado (Bruno, 14/10/2026: reverter depois da medição das 23h; ver Consequências) · **Data:** 14/10/2026 · **Origem:** teste de carga `k6-put-get-load` de 14/10, às 22h · **Revê:** a alternativa "ACK esperando só o próprio item", descartada no [ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md) · **Precisa:** [ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md), [ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md), [ADR-057](ADR-057-um-unico-modo-strong-consistency.md)

## Contexto

No teste de PutItem seguido de GetItem forte, com rampa até 100 pares/s contra a líder, o cluster entregou só cerca de 44 pares/s. A latência subiu para p50 de 2 s e p99 de 5 s no PutItem e no GetItem, sem nenhum erro nem throttling.

- O Raft estava saudável: laço p99 de 7 ms, fsync do WAL p99 de 10 ms, ticks em 109 ms e nenhuma troca de líder.
- O banco da líder estava lento: o item no LocalStack da cell-1 teve p99 de 1 a 2,3 s, contra cerca de 0,65 s nas followers. O LocalStack da líder também atende todos os GetItem.
- O atraso do apply nunca passou de 54 entradas, contra `APPLY_LAG_MAX` = 4.096.

A causa da latência é a marca d'água **contígua** do [ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md). O ACK e a barreira da leitura forte esperam `applied >= índice`, e `applied` só passa de uma entrada quando todas as anteriores, de **qualquer** chave, estão no banco. Um único item lento segura o ACK e o GetItem de todas as escritas posteriores, mesmo as de chaves em outros workers. Com 16 workers, a cauda de um único worker vira a latência do shard inteiro.

O ADR-058 já tinha registrado o "ACK esperando só o próprio item" como alternativa, para avaliar com a medição do S5. O teste de 14/10 é essa medição.

## Decisão

1. **Cada worker do apply é um slot com a sua marca d'água.** A marca do slot é o maior índice tal que todos os itens daquele slot até ele estão no banco **e** o despacho já passou dele. Sem item a caminho, a marca do slot é o último índice despachado; com item a caminho, é o índice anterior ao primeiro deles. Entradas sem item no slot (noop, ConfChange, itens de outras chaves) avançam o slot junto com o despacho.
2. **O ACK espera a marca do slot de cada chave escrita.** Com uma chave, é um slot; com um `BatchWriteItem`, são os slots de todas as chaves da entrada. Sem itens, espera a marca d'água global.
3. **O GetItem espera a marca do slot da chave lida**, até `min(ReadIndex, gate)`, como no [ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md).
4. **Continuam na marca d'água global:** o Scan (lê várias chaves), a admissão por atraso do apply ([ADR-060](ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)), o progresso salvo e o replay ([ADR-048](ADR-048-skip-por-item.md)), o `/status` e as métricas `dr_applied_index` e `dr_apply_lag`.
5. **Item venenoso ([ADR-038](ADR-038-skip-de-entrada-venenosa-sempre-humano.md)):** o slot do item venenoso para logo antes dele, porque o item nunca sai da fila do slot. Os itens descartados depois do estacionamento também não saem, então nenhum slot passa de um item que não está no banco. Os outros slots seguem até o fim do que foi despachado.

### Por que continua linearizável

A mesma chave sempre cai no mesmo slot (`keySlot`, FNV-1a de tabela e chave), e o worker do slot grava na ordem do despacho, que é a ordem do log. Então "marca do slot da chave `k` >= `b`" implica que **todas** as escritas em `k` com índice <= `b` estão no banco. É exatamente o que a leitura forte de `k` precisa. Nada é despachado antes do gate, então a marca do slot nunca passa do gate ([ADR-057](ADR-057-um-unico-modo-strong-consistency.md)). A leitura pode devolver um valor de índice acima da barreira, como já acontecia no ADR-058.

## Alternativas consideradas

- **ACK esperando só a própria entrada (`ent.left == 0`):** tira a espera do ACK, mas o GetItem logo depois continuaria esperando a marca d'água global e ficaria lento do mesmo jeito. Descartado: resolve só metade do problema.
- **ACK depois de commit e gate, sem esperar o apply:** muda a regra do 6.9, item 4 (o banco da líder reflete a escrita no ACK), e passa toda a latência do banco para a leitura forte. A admissão viraria o único freio dos clientes. Descartado; é decisão de requisito, não de desempenho.
- **Marca d'água por chave:** exata, mas exige um mapa por chave com limpeza. O slot dá o mesmo ganho com memória fixa (um por worker). Descartado.

## Consequências

- **Um item lento só atrasa as chaves do próprio slot**, cerca de 1/`APPLY_WORKERS` das chaves, em vez do shard inteiro. O bloqueio na cabeça da fila dentro do slot continua, como no ADR-058.
- **Duas regras de "aplicado"** passam a conviver: a marca do slot (ACK e GetItem) e a marca d'água global (Scan, admissão, progresso e status). É o custo que o ADR-058 queria evitar; ele fica restrito ao applier e à porta `AppliedWaiter`.
- **Read-your-writes entre chaves diferentes não muda:** um GetItem de `k2` depois do ACK de `k1` espera a barreira, que é >= o índice de `k1`, mas só no slot de `k2`. Como as escritas em chaves diferentes não têm relação de ordem visível pela API além do índice, isso continua linearizável por chave, que é o que o GetItem promete. O Scan, que vê várias chaves, continua na marca global.
- **O Scan não melhora:** continua esperando a marca d'água global.
- **A capacidade não muda:** o teto continua sendo a vazão do banco. Muda a latência abaixo do teto e quanto a cauda de um item espalha pelo shard.
- **Medição de 14/10, 23h** (`k6-put-get-load`, mesmos volumes, comparado com as rodadas das 22:19 e 22:24, sem A2):
  - **Abaixo da saturação melhora.** No warmup, ACK p99 de 106–129 ms para 56 ms, GetItem p99 de 41–72 ms para 20 ms e barreira p99 de 16–25 ms para 2 ms. Na rampa, barreira p99 de 452–517 ms para 291 ms.
  - **Na saturação não melhora e a cauda piora.** Vazão igual (cerca de 40 pares/s), ACK p50 igual (cerca de 2,2 s), ACK p99 de 4,9 s para 9,2 s e 3 PutItem PENDING (prazo de 7,5 s), contra 0–1 antes.
  - **Leitura corrigida da causa:** o teto é a vazão do apply (16 workers ÷ cerca de 0,36 s por item no LocalStack ≈ 44 itens/s). Acima dele a fila cresce nos workers e a latência vira fila ÷ vazão. A marca d'água global repartia essa espera por igual entre todos; a marca por slot a concentra nos slots mais cheios, daí a cauda maior.
  - **Consequência:** o ADR-061 só vale junto com uma admissão que recuse antes da fila passar do prazo, ou seja, `APPLY_LAG_MAX` derivado da vazão e do prazo, e não o padrão de 4.096. A validar com `task start APPLY_LAG_MAX=<baixo>`.

## Código

- `DataReplicationApp/internal/usecase/apply.go`: `slotMark`, `dispatched`, `track` e `done` por slot, `WaitAppliedKey`.
- `DataReplicationApp/internal/port/ports.go`: `AppliedWaiter.WaitAppliedKey`.
- `DataReplicationApp/internal/usecase/write.go`: `waitApplied` por chave escrita.
- `DataReplicationApp/internal/usecase/read.go`: GetItem pelo slot da chave, Scan pela marca global.
- Testes: espera por chave não segura outro slot e segura o mesmo slot, a mesma chave espera as escritas anteriores, noop avança o slot, item venenoso para só o slot dele, o ACK espera cada chave escrita, o GetItem espera a chave e o Scan a marca global.

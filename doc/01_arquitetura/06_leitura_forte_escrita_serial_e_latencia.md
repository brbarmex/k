# Orçamento de latência, escrita serial e leitura forte

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 7. Orçamento de latência

A premissa A1 foi retirada (Bruno, 12/10): a tabela abaixo é só um alvo de trabalho e **não** diz se a escrita no DynamoDB conta nos 50 ms. Reavaliar nos testes de carga.

| Etapa | Meta P99 (a medir) | Observação |
|---|---|---|
| Parse, validação e hash (módulo `api`) | ≤ 1 ms | JSON do DynamoDB é o principal custo; usar parser rápido (H-DR6) |
| App → `DataReplication` (Service local) | ≤ 1 ms | +1–2 ms se a célula não for a líder (forward) |
| Dedupe + proposta + fsync local do líder | ≤ 3 ms | Group commit por shard, sem espera fixa; com F3 corre **em paralelo** às réplicas e só pesa se for o mais lento ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)) |
| Réplica AWS (outra AZ) + fsync | ≤ 3 ms | Paralelo à Azure |
| Réplica Azure (RTT cross-cloud + fsync) | **RTT medido + 2–3 ms** | Provavelmente o maior item de overhead |
| Gate + disparo do applier | ≤ 1 ms | |
| **Escrita no DynamoDB** | P99 do DynamoDB | Fora desta soma por enquanto; a reavaliar nos testes |
| Resposta à aplicação | ≤ 1 ms | |
| **Total de overhead** | **≈ 15–20 ms + RTT Azure** | Margem para os 50ms. **Soma de metas por etapa, não o P99 da operação** (ver abaixo) |

**Apply só depois do gate ([ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md)):** o applier só roda **depois** do gate, ou seja, depois da Azure, **em todas as instalações**. Rodar a escrita no DynamoDB em paralelo à confirmação da Azure economizaria um RTT, mas um leitor veria um dado que ainda **não está durável na Azure**, e uma perda da AWS logo em seguida criaria um RPO > 0 sem aviso. Não há modo alternativo: se `t_apply` estourar o orçamento (spike S5), o ajuste é no banco ou no orçamento, não na garantia.

**SLO ponta a ponta, medido sobre a operação completa (decisão 13, 13/10).** A tabela acima é um orçamento por etapa, útil para achar onde o tempo vai; ela **não** é o contrato. O contrato é um SLO ponta a ponta de **latência, erro e recuperação**, medido sobre a operação completa, da chegada da requisição à resposta ao cliente, com os tempos `t_parse`, `t_raft`, `t_azure` e `t_apply` registrados à parte. Somar percentis de etapas não dá o percentil da soma. Ao cliente publica-se `overhead + escrita no banco` ([ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)), e o P99 do banco entra no contrato. Os números só são fixados depois do spike S1 e dos testes de carga.

**RTO como distribuição medida em exercícios.** O RTO de um cenário não é a soma dos P99 de etapas sequenciais: é a **distribuição medida em exercícios de falha** (mediana, P95 e pior caso), repetidos em game days. Os componentes servem para diagnosticar, não para prometer. Há um piso que não vem de medição: com `DEGRADED_POLICY=accept`, se a AWS morre em modo degradado, o RTO é **pelo menos D + M** ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md); [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md), [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md)).

**F3 no orçamento (ADR-056).** Com a líder enviando `MsgApp` antes de gravar, o tempo até o gate é `max(t_L, RTT_A + t_S, RTT_Z + t_Z)` em vez de `t_L + max(RTT_A + t_S, RTT_Z + t_Z)`. O apply ainda espera o disco da líder. O `AsyncStorageWrites` tem o mesmo ganho de latência e só entra se o spike S1b mostrar ganho de P99 ou robustez a fsync lento.

**Outros ganhos possíveis:** `ReadIndex` em lote (uma confirmação para várias leituras fortes, sempre limitada por `min(ReadIndex, gate)`, 7.2), e *pipelining* de propostas do mesmo shard.

---

### 7.1 Um único modo: escrita serial depois do gate ([ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md))

O `DataReplication` tem **um único modo, strong consistency**, na escrita, na replicação e no Raft. A líder replica, espera o gate (maioria AWS + maioria Azure, com fsync) e só então aplica no banco da célula; o ACK sai depois do apply. Não existe apply antes do gate, modo por tabela nem modo por instalação. Dentro dessa regra, o applier grava **em paralelo por chave** (`APPLY_WORKERS`, padrão 16), com a mesma chave na ordem do log, e o `applied` é a **marca d'água contígua** ([ADR-058](../03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)). Com um item por vez, um `PutItem` de 4 a 8 ms limitaria o shard a 125 a 250 itens/s.

**Consequências:**
1. O ACK inclui `t_apply` (de um item, mais a espera da marca d'água) em todos os casos; o SLO publicado como overhead + banco ([ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)) já absorve isso.
2. `applied` nunca passa do gate. Isso sustenta a barreira `min(ReadIndex, gate)` da leitura (7.2, [ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)) e a invariante I-DEL (05, 6.4.3).
3. O applier aplica as operações da mesma chave na ordem do log (05, 6.4.1) e o `appliedIndex` só avança até o último índice **contíguo** aplicado. A degradação da Azure é tratada pelo lease (5.3.2), nunca por um apply antes do gate.

### 7.2 Leitura: sempre forte ([ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md))

- As aplicações enviam `ConsistentRead=true`. Uma leitura de tabela base que chegue sem o parâmetro é servida como forte (não existe outro caminho) e conta na métrica `read_without_consistent_read`.
- **`Query` e `Scan` em GSI são recusados** com `ValidationException` (400) ([ADR-050](../03_adr/ADR-050-query-em-gsi-recusada.md)).
- Nenhuma leitura é servida sem a barreira: se ela não é alcançada no prazo, a resposta é `503` recuperável.

**Barreira: `min(ReadIndex, gate)` ([ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)).** A leitura espera que o `appliedIndex` da célula alcance **`min(ReadIndex, gate)`**, e não só o `ReadIndex` do commit do Raft. Motivo: o apply só roda depois do gate; com a barreira antiga, uma Azure lenta atrasava as leituras mesmo sem escrita pendente. Como um cliente só pode ter visto escritas já ACKadas, e toda escrita ACKada está abaixo do gate, o `min` mantém a leitura-do-que-escrevi e desacopla a leitura da saúde da Azure.
- Na célula follower, a líder devolve o `ReadIndex` **e** o gate na mesma resposta. Um gate antigo guardado na follower enfraqueceria a garantia.
- A líder recém-eleita só atende leitura **depois de reconstruir o gate** ([ADR-029](../03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md), [ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)).
- Com o applier parado por entrada venenosa, a leitura do shard **falha rápido com `503`** em vez de esperar o timeout.
- Células da Azure antes da promoção seguem a mesma regra; sem líder alcançável (modo degradado), `503`.
- Custo: soma ~1 RTT ao líder na célula seguidora, e no DynamoDB sob demanda a leitura forte consome o dobro de unidades de leitura.
- Cenários: [C05](../02_cenarios/atualizados/C05-leitura-strong.md) e [R16](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md).

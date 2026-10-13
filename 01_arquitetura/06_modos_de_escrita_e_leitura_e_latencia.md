# Orçamento de latência, escrita serial e modos de leitura Strong e Eventual

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 7. Orçamento de latência

A premissa A1 foi retirada (Bruno, 12/10): a tabela abaixo é só um alvo de trabalho e **não** diz se a escrita no DynamoDB conta nos 50 ms. Reavaliar nos testes de carga.

| Etapa | Meta P99 (a medir) | Observação |
|---|---|---|
| Parse, validação e hash (módulo `api`) | ≤ 1 ms | JSON do DynamoDB é o principal custo; usar parser rápido (H-DR6) |
| App → `DataReplication` (Service local) | ≤ 1 ms | +1–2 ms se a célula não for a líder (forward) |
| Dedupe + proposta + fsync local do líder | ≤ 3 ms | Group commit |
| Réplica AWS (outra AZ) + fsync | ≤ 3 ms | Paralelo à Azure |
| Réplica Azure (RTT cross-cloud + fsync) | **RTT medido + 2–3 ms** | Provavelmente o maior item de overhead |
| Gate + disparo do applier | ≤ 1 ms | |
| **Escrita no DynamoDB** | P99 do DynamoDB | Fora desta soma por enquanto; a reavaliar nos testes |
| Resposta à aplicação | ≤ 1 ms | |
| **Total de overhead** | **≈ 15–20 ms + RTT Azure** | Margem para os 50ms. **Soma de metas por etapa, não o P99 da operação** (ver abaixo) |

**Decisão sobre o apply (ADR-053, 13/10):** o applier só roda **depois** do gate, ou seja, depois da Azure, **em todas as instalações**. Rodar a escrita no DynamoDB em paralelo à confirmação da Azure economizaria um RTT, mas um leitor veria um dado que ainda **não está durável na Azure**, e uma perda da AWS logo em seguida criaria um RPO > 0 sem aviso. O apply paralelo só volta como opt-in, com contrato "pode ler escrita não confirmada", se a medição (spike S5) mostrar que `t_apply` estoura o orçamento.

**SLO ponta a ponta, medido sobre a operação completa (decisão 13, 13/10).** A tabela acima é um orçamento por etapa, útil para achar onde o tempo vai; ela **não** é o contrato. O contrato é um SLO ponta a ponta de **latência, erro e recuperação**, medido sobre a operação completa, da chegada da requisição à resposta ao cliente, com os tempos `t_parse`, `t_raft`, `t_azure` e `t_apply` registrados à parte. Somar percentis de etapas não dá o percentil da soma. Ao cliente publica-se `overhead + escrita no banco` ([ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)), e o P99 do banco entra no contrato. Os números só são fixados depois do spike S1 e dos testes de carga.

**RTO como distribuição medida em exercícios.** O RTO de um cenário não é a soma dos P99 de etapas sequenciais: é a **distribuição medida em exercícios de falha** (mediana, P95 e pior caso), repetidos em game days. Os componentes servem para diagnosticar, não para prometer. Há um piso que não vem de medição: com `DEGRADED_POLICY=accept`, se a AWS morre em modo degradado, o RTO é **pelo menos D + M** ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md); [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md), [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md)).

**Outros ganhos possíveis:** `ReadIndex` em lote (uma confirmação para várias leituras fortes, sempre limitada por `min(ReadIndex, gate)`, 7.2), e *pipelining* de propostas do mesmo shard.

---

### 7.1 Escrita sempre serial; o Eventual é só de leitura (ADR-053, substitui a decisão de 12/10)

**Decisão atual:** a escrita é a mesma em todas as instalações. A líder replica, espera o gate (maioria AWS + maioria Azure) e só então aplica no banco da célula; o ACK sai depois do apply. **O apply é sempre serial e depois do gate.** O modo Eventual (E1, [ADR-009](../03_adr/ADR-009-eventual-e1.md)) muda **só a leitura** (7.2).

**O que saiu da v1:**
- O **apply paralelo** do Eventual (escrever no banco assim que a AWS confirma, em paralelo à Azure). Um leitor podia ver uma escrita ainda não durável na Azure, que nunca seria ACKada se a AWS caísse antes, e quem leu agiu sobre um valor que depois "não aconteceu". O ganho era esconder `t_apply` (alguns ms) atrás da Azure; o risco é desproporcional.
- A variável **`WRITE_MODE`**, o requisito de que o modo fosse igual nas células do grupo e o alerta de divergência dele. Não há mais dois caminhos no applier.

**Consequências:**
1. O ACK inclui `t_apply` em todos os casos; o SLO publicado como overhead + banco ([ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)) já absorve isso.
2. `applied` nunca passa do gate. Isso sustenta a barreira `min(ReadIndex, gate)` da leitura forte (7.2, [ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)) e a invariante I-DEL (05, 6.4.3).
3. O applier continua aplicando as operações da mesma chave na ordem do log (05, 6.4.1) e o `appliedIndex` só avança até o último índice **contíguo** aplicado. A falha da Azure depois da gravação deixou de existir como caso, porque o apply só acontece depois do gate (a degradação é tratada pelo lease, 5.3.2).
4. O cenário [C04](../02_cenarios/atualizados/C04-escrita-eventual.md) descreve o Eventual de leitura, sem escrita paralela.

**Reavaliação:** o apply paralelo pode voltar como opt-in por instalação, com o contrato "pode ler escrita não confirmada", só se a medição do spike S5 mostrar que `t_apply` estoura o orçamento.

### 7.2 Modo de leitura: padrão forte (decisão do Bruno, 12/10; barreira revista em 13/10)

O `ConsistentRead` do SDK será usado para pedir leitura forte, **mas não pode ser a única regra**, porque o cliente pode esquecê-lo. Sem a flag, o SDK pede leitura eventual por padrão, e uma app descuidada leria dado atrasado sem perceber.

**Decidido pelo Bruno (12/10):**
- Variável de ambiente obrigatória: `READ_MODE_DEFAULT=strong|eventual` (o app não sobe sem ela).
- `ConsistentRead=true` **sempre** vira leitura forte, **na tabela base**.
- `ConsistentRead` ausente ou `false` segue o `READ_MODE_DEFAULT`. Com o padrão `strong`, uma app que esquece a flag recebe leitura forte; a leitura eventual só existe onde a configuração a permitir.
- **Em GSI não há leitura forte** ([ADR-050](../03_adr/ADR-050-gsi-nao-tem-leitura-forte.md)): `ConsistentRead=true` em `Query` de GSI é recusada com `ValidationException` (400), como no DynamoDB. Sem o parâmetro, `READ_MODE_DEFAULT=strong` vale só para a tabela base; em GSI a leitura é sempre eventual, com o header `x-dr-consistency: eventual`.

**Barreira da leitura forte: `min(ReadIndex, gate)` ([ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)).** A leitura forte espera que o `appliedIndex` da célula alcance **`min(ReadIndex, gate)`**, e não só o `ReadIndex` do commit do Raft. Motivo: o apply só roda depois do gate; com a barreira antiga, uma Azure lenta atrasava as leituras fortes mesmo sem escrita pendente. Como um cliente só pode ter visto escritas já ACKadas, e toda escrita ACKada está abaixo do gate, o `min` mantém a leitura-do-que-escrevi e desacopla a leitura da saúde da Azure.
- Na célula follower, a líder devolve o `ReadIndex` **e** o gate na mesma resposta. Um gate antigo guardado na follower enfraqueceria a garantia.
- A líder recém-eleita só atende leitura forte **depois de reconstruir o gate** ([ADR-029](../03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md), [ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)).
- Com o applier parado por entrada venenosa, a leitura forte do shard **falha rápido com `503`** em vez de esperar o timeout.
- Custo: soma ~1 RTT ao líder na célula seguidora. O Eventual lê a tabela local e informa o atraso (E1).
- Cenários: [C05](../02_cenarios/atualizados/C05-leitura-strong-e-eventual.md) e [R16](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md).

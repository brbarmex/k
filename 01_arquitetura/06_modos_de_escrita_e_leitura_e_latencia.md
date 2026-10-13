# Orçamento de latência e modos Strong e Eventual

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
| **Total de overhead** | **≈ 15–20 ms + RTT Azure** | Margem para os 50ms |

**Tradeoff que o Bruno deve conhecer:** o applier só roda **depois** do gate, ou seja, depois da Azure. A escrita no DynamoDB poderia rodar em paralelo à confirmação da Azure e economizar um RTT. Mas então uma leitura forte veria um dado que ainda **não está durável na Azure**, e uma perda da AWS logo em seguida criaria um RPO > 0 sem aviso. Recomendo **serial na v1**, medir, e só então avaliar a versão paralela como otimização (D5).

**Outros ganhos possíveis:** `ReadIndex` em lote (uma confirmação para várias leituras fortes), e *pipelining* de propostas do mesmo shard.

---

### 7.1 Modo de escrita: Strong serial, Eventual paralelo (decisão do Bruno, 12/10)

**Decisão:** no modo **Strong**, a escrita no banco da célula líder só começa depois do gate (maioria AWS + maioria Azure). No modo **Eventual**, a escrita no banco começa assim que a AWS confirma, **em paralelo** à confirmação da Azure. **Nos dois modos o ACK só sai depois da confirmação da Azure** (RPO = 0 do que foi ACKado). O que o Eventual muda é só a janela em que o dado fica visível no banco antes de estar durável na Azure.

**Como o modo é escolhido (decisão do Bruno):** por **variável de ambiente** do `DataReplication`, aplicada a todas as tabelas daquele processo. A variável é **obrigatória**: sem ela, ou com valor inválido, o app **não sobe**. Não há valor padrão. A configuração geral vem de um arquivo YAML carregado com a lib `viper` (ver 10.1).

**Aceito pelo Bruno (12/10):** no modo Eventual, o leitor pode ver um dado à frente da durabilidade da Azure por poucos ms.

**Consequências e cuidados:**
1. **O modo é por processo, não por tabela.** Todas as tabelas de uma instância têm o mesmo modo.
2. **O modo deve ser igual nas células de um grupo.** O modo determina o comportamento da célula **líder**, e a liderança muda. Se uma célula tiver modo diferente, o comportamento muda silenciosamente quando ela virar líder. Na conexão entre células, trocar o valor do modo e exportar uma métrica/alerta de divergência.
3. **Leitores veem dado "adiantado" no Eventual.** Isso não é o atraso (staleness) que o E1 já aceita: é ver algo à frente da durabilidade. Se a AWS inteira cair dentro desse intervalo (poucos ms), esse dado some. Aceitável só onde isso for tolerado.
4. **Ordem por chave no applier paralelo.** Com várias escritas em voo, a condição de versão (6.4) descarta a que chegar atrasada para a mesma chave. O `appliedIndex` só avança até o último índice **contíguo** aplicado, para a barreira das leituras fortes continuar correta.
5. **Falha da Azure depois da gravação.** Não dá para desfazer a gravação já feita no banco. O desfecho segue o modo degradado (opção B).
6. **Dois caminhos no applier.** Recomendo implementar o serial primeiro e o paralelo depois, atrás do valor da variável, medindo o ganho real nos testes de carga.

### 7.2 Modo de leitura: padrão forte (decisão do Bruno, 12/10)

O `ConsistentRead` do SDK será usado para pedir leitura forte, **mas não pode ser a única regra**, porque o cliente pode esquecê-lo. Sem a flag, o SDK pede leitura eventual por padrão, e uma app descuidada leria dado atrasado sem perceber.

**Decidido pelo Bruno (12/10):**
- Variável de ambiente obrigatória, como a do modo de escrita: `READ_MODE_DEFAULT=strong|eventual` (o app não sobe sem ela).
- `ConsistentRead=true` **sempre** vira leitura forte.
- `ConsistentRead` ausente ou `false` segue o `READ_MODE_DEFAULT`. Com o padrão `strong`, uma app que esquece a flag recebe leitura forte; a leitura eventual só existe onde a configuração a permitir.
- Custo: a leitura forte espera a barreira de `appliedIndex` (ReadIndex), que soma ~1 RTT ao líder na célula seguidora. O Eventual lê a tabela local e informa o atraso (E1).

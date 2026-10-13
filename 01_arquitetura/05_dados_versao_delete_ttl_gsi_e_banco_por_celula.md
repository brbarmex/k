# Dados: versão, DELETE, TTL, Scan, GSI e banco por célula

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

### 6.4 Post-image e versão (contrato com o applier)
**Confirmado pelo Bruno (12/10):** o Cloud-Adapter suporta *conditional expression*, então a condição de versão (`versão < índice`) também vale no lado Azure.

- O WAL grava a **imagem pós-escrita** e a **versão = índice Raft**. O applier escreve no banco com condição "versão armazenada < versão da entrada". Isso torna o *replay* **idempotente** e rejeita aplicação fora de ordem de um applier antigo.
- **Atenção (achado da revisão de 13/10):** o `BatchWriteItem` do DynamoDB **não aceita expressão de condição**. A condição de versão só vale em `PutItem` e `DeleteItem` individuais (ou em `TransactWriteItems`, que custa o dobro). Por isso o applier grava um lote item a item, de forma condicional, com o lote como uma única entrada ([ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md), [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md)).
- O atributo de versão é um atributo **reservado** no item (nome a definir, ex.: `_dr_v`). Ele é visível para a aplicação e precisa estar documentado como proibido de uso.
- **DELETE** precisa de tratamento: apagar o item perde a versão, e uma escrita antiga em voo poderia recriá-lo ("zumbi"). Isso só acontece se duas operações da mesma chave puderem ser aplicadas fora de ordem, o que passa a ser possível com o apply paralelo do modo Eventual (7.1) e em reaplicações após queda. Avaliação das opções em 6.4.1.
- **Por isso o escopo da v1 recusa UpdateItem com expressão e escrita condicional:** calcular a post-image exige ler o estado atual e serializar por chave, o que muda o custo e a latência. Fica para a v1.1.

### 6.4.1 DELETE: tombstone com TTL × DELETE real com ordem por chave

**O problema.** A condição de versão (`v < índice`) protege o replay, mas depois de um DELETE o item não existe mais e a versão se perde. Se um PUT antigo da mesma chave ainda estiver em voo, ele **recria o item** (zumbi). Isso exige operações da mesma chave aplicadas fora de ordem, o que o **apply paralelo do Eventual** (7.1) torna possível.

| Opção | Como funciona | Vantagens | Desvantagens |
|---|---|---|---|
| **A. Tombstone com TTL** (descartada) | O DELETE vira um PUT condicional de um marcador (`_dr_deleted`, `_dr_v`, atributo de TTL). O DynamoDB apaga o marcador depois do TTL. | Resolve o zumbi mesmo com ordem trocada e em reconstrução a partir de cópia antiga | **O marcador vaza para os clientes**: o GetItem devolveria o marcador, e Query/Scan/BatchGet também, então o `DataReplication` precisa filtrar tudo (o `FilterExpression` roda depois da leitura: custo maior e páginas curtas ou vazias). Quem consome **DynamoDB Streams** vê um MODIFY no lugar de um REMOVE, e depois um REMOVE do TTL. **Uma tabela só tem um atributo de TTL**: se o cliente já usa o dele, entra em conflito. O TTL do DynamoDB apaga com atraso (até dias). |
| **B. DELETE real + ordem por chave no applier** (adotada) | O applier garante que as operações da **mesma chave** rodam uma de cada vez, na ordem do log (fila por chave ou hash da chave em N filas); chaves diferentes seguem em paralelo. O DELETE é um DeleteItem comum. | Nada vaza para os clientes (Streams, TTL, Scan, GSI ficam como estão). Sem atributo extra para apagar. Simples de explicar | Não protege contra reaplicar um trecho antigo do log sobre um estado mais novo **fora de ordem** (a reaplicação em ordem converge). Exige disciplina na reconstrução de réplica |
| **C. DELETE condicional por versão** | `DeleteItem` com condição de versão menor | Não deixa um DELETE antigo apagar um item mais novo | Sozinho **não** impede o PUT antigo de recriar o item. Complementa B, não substitui |
| **D. Misto** | Tombstone só nas tabelas em modo Eventual; DELETE real com ordem por chave nas Strong | Menor vazamento | Dois comportamentos; como o modo é por processo (7.1), na prática fica igual a A ou B |

**Detalhe da opção B (reaplicação após queda):** o `appliedIndex` persistido pode estar atrás do estado real do banco. Na reinício, o applier reaplica o trecho do log **em ordem**: o PUT recria o item e o DELETE seguinte o apaga de novo, e o resultado converge. Durante esse replay, um leitor pode ver o item reaparecer por instantes. Para não violar a leitura forte, as leituras só abrem depois que o applier alcançar o ponto mais alto que já tinha aplicado antes da queda (guardado em arquivo local, melhor esforço).

**Perguntas para decidir (Bruno):** os clientes usam (a) DynamoDB Streams, (b) o próprio atributo de TTL nas tabelas, (c) Scan ou GSIs nessas tabelas? Se a resposta for sim para qualquer uma, a opção A fica ainda pior.

**Uso real informado pelo Bruno (12/10):** alguns clientes usam **TTL** na tabela, poucos usam **Scan** e algumas **Query** usam **GSI**. Isso confirma a recomendação abaixo, porque o tombstone entraria em conflito com o TTL do cliente (uma tabela só tem um atributo de TTL) e sujaria o Scan e os GSIs.

**Recomendação:** B na v1, com C como reforço. A opção A volta para a discussão só se a reconstrução de réplica a partir de uma cópia antiga do banco se mostrar necessária (H-DR1).

### 6.4.2 Impacto do TTL, Scan e GSI dos clientes

> **Confirmado pelo Bruno (12/10):** (1) o Cloud-Adapter traduz o TTL do DynamoDB para o TTL do Cosmos DB; (2) a expiração por TTL não é replicada pelo log, cada célula expira localmente; (3) Query em GSI com leitura forte vale só até a propagação do índice.


**TTL do cliente (a expiração é local a cada célula).** O DynamoDB apaga itens expirados por conta própria, em cada tabela, **sem passar pelo nosso log**. Consequências:
- Cada célula expira o mesmo item em momentos ligeiramente diferentes (a remoção pode demorar até dias depois da hora de expiração), então uma leitura pode ainda devolver o item em uma célula e não em outra. As apps já precisam ignorar itens expirados ainda presentes, por causa da remoção tardia do DynamoDB.
- Reaplicar um PUT antigo (replay) pode trazer de volta um item que já tinha expirado; ele volta com o TTL no passado e é removido de novo pelo DynamoDB. As apps que filtram itens expirados não percebem.
- **Decisão proposta:** a expiração por TTL **não é replicada** pelo log; cada célula expira localmente e os resultados convergem. Registrar essa semântica para os clientes.
- **Dependência da Azure:** o TTL do DynamoDB é um atributo com data/hora em segundos; o Cosmos DB tem TTL próprio, com semântica diferente. O Cloud-Adapter precisa traduzir um no outro. **Sem isso, os itens nunca expiram na Azure**, e depois de um failover itens expirados voltam a parecer vivos. **Confirmado pelo Bruno (12/10): o Cloud-Adapter traduz o TTL.**

**Scan.** Funciona normalmente com DELETE real (nada de marcador). Uma leitura forte de Scan espera a barreira antes de começar, mas o DynamoDB não dá um instantâneo entre as páginas do Scan: as páginas seguintes podem ver escritas mais novas. Documentar.

**Query em GSI.** O DynamoDB **não aceita `ConsistentRead=true` em GSI** (os índices são eventualmente consistentes). Com `READ_MODE_DEFAULT=strong` (7.2), o `DataReplication` aplica a barreira de `appliedIndex` da tabela base e envia a consulta ao GSI **sem** a flag. A leitura é "forte até o ponto em que o DynamoDB propaga o GSI" (em geral milissegundos), e não garante ler a escrita mais recente. Documentar e, se algum cliente precisar de garantia total, consultar a tabela base.

### 6.9 Um banco por célula: nome lógico × nome físico (confirmação do Bruno, 12/10)
**Capacidade (Bruno, 12/10):** tabelas DynamoDB em modo **sob demanda**; Cosmos DB com **RU**. Cada célula fica em uma das **3 zonas** (AZ) da região. Cuidado: o modo sob demanda também tem limite de escala por tabela e pode limitar (throttling) em picos súbitos acima do pico anterior; monitorar o lag de apply por célula.


**Fato:** cada célula tem o seu próprio DynamoDB (e a Azure, o seu Cosmos). A célula `cell-120321` tem tabelas como `cliente_cell_120321`. As apps da célula chamam o `DataReplication` usando o nome físico da tabela dela.

**Consequências para o desenho:**
1. **O log replicado precisa de um nome lógico de tabela.** Se a entrada levasse `cliente_cell_120321`, as outras células aplicariam na tabela errada ou em tabela inexistente. O `DataReplication` converte o nome físico em **lógico** (`cliente`) ao aceitar a escrita, e cada applier converte o lógico no físico da **sua** célula (`cliente_cell_<idDaMinhaCélula>`). O shard é definido pelo nome lógico.
2. **Como derivar o nome lógico (decisão do Bruno, 12/10).** O formato `<nome>_cell_<id>` não pode ser imposto, mas o `<id>` é informado no deploy e chega ao `DataReplication` como parâmetro (`CELL_ID`). Proposta: **nome lógico = nome físico com o `CELL_ID` trocado pelo marcador `{cell}`**; na hora de aplicar, o marcador é trocado pelo id da célula local. Exemplo: `cliente_cell_120321` → `cliente_cell_{cell}` → na célula 120322 vira `cliente_cell_120322`. Não é preciso conhecer o formato do nome, só que o id apareça nele. O marcador é seguro porque `{` e `}` não são permitidos em nomes de tabela do DynamoDB.
   Cuidados:
   - **Fronteira do id:** só trocar quando o id estiver delimitado (início/fim do nome ou separado por `_`, `-` ou `.`). Senão `120` casaria dentro de `1203`, ou um dígito do nome lógico casaria com o id.
   - **Exatamente uma ocorrência.** Zero ocorrências (não dá para derivar) ou mais de uma (ambíguo) → a requisição é **recusada** com erro claro, nunca adivinhada. Isso também barra o acesso a tabelas de outra célula.
   - **Lista permitida de tabelas lógicas** em configuração. Só o que está na lista é replicado; o resto é recusado. Evita replicar tabela por engano.
   - **Validação na partida:** o `DataReplication` confere se as tabelas físicas da lista existem na célula local (`DescribeTable`) e falha cedo se faltar alguma. A mesma lista tem de ser igual em todas as células, e isso entra na checagem de configuração entre células.
   - **Leituras** vão direto para a tabela física local, sem tradução; a mesma regra de "id da própria célula" vale.
   - **A conversão ainda depende de ler o nome da tabela no corpo** (H-DR6).
3. **Um único escritor por tabela.** Cada tabela só recebe escrita do applier da própria célula, na ordem do log, com versão condicional. Isso **simplifica o problema do fencing do DynamoDB**: um applier antigo não disputa a mesma tabela com o novo, porque são tabelas diferentes. A proteção por versão (6.4) continua valendo para o replay.
4. **Escrita multiplicada.** Cada escrita lógica vira uma escrita física em **cada célula** (3 na AWS e 3 na Azure). O ACK espera só o apply da célula líder; as demais aplicam em seguida. O custo de WCU/RU e a possibilidade de **throttling** em uma célula (que aumenta o lag de apply dela) entram no planejamento de capacidade (X14).
5. **Leituras.** Uma leitura forte numa célula seguidora espera o `appliedIndex` **daquela** célula alcançar o índice da barreira. Uma leitura eventual lê a tabela local direto e informa o atraso (E1).
6. **Reconstrução de réplica (6.6).** Como cada célula tem seu banco, reconstruir uma célula é recriar as tabelas dela a partir de uma cópia de outra célula e reaplicar o log. O nome físico faz parte da configuração da célula, não do log.
7. **IAM.** Cada célula tem política própria: só o papel do `DataReplication` daquela célula escreve nas tabelas dela.

**Resolvido (Bruno, 12/10):** as células de um grupo de replicação têm **todas as mesmas tabelas lógicas** (cenário A). A lista permitida é única para o grupo, e a checagem de configuração entre células exige que seja idêntica.

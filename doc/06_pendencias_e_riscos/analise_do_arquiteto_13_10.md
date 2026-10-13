# Resposta à revisão crítica do DataReplication v1

**Data:** 13/10/2026 · **Autor:** agente arquiteto · **Sobre:** o relatório "Revisão crítica de arquitetura" do Bruno.
**Registro histórico.** Este arquivo é a análise feita antes das decisões. O Bruno decidiu **a em todas as 14** (na 11, manter um banco por célula) e o pacote já foi atualizado: veja [revisao_critica_13_10](revisao_critica_13_10.md) e os ADRs 044 a 054. Os links de ADRs e cenários abaixo apontam para o estado de hoje.

## 1. Posição geral

Concordo com o parecer: **arquitetura candidata para prototipação, não para produção**. O pacote nunca disse que estava pronto (a definição de pronto da seção 11 ainda é uma lista de coisas a provar), mas o relatório acerta ao dizer que o texto descreve bem o caminho feliz e a reação às falhas, e **ainda não prova os invariantes**. Para dados monetários essa diferença decide.

Conferi no código local os oito defeitos apontados no Go: **todos existem**. Conferi também as três discordâncias de documentação (ADR-035 contra a tabela de structs, definição de pronto contra os fluxos humanos, R18): **existem**.

| Ponto do relatório | Minha posição | Resumo |
|---|---|---|
| P0-1 e 3.1 Fencing e split-brain | **Concordo** e acrescento uma saída | O problema vem do próprio modo degradado. Proponho lease de degradação (decisão 1) |
| P0-2 e 3.2 Prefixo do gate | **Concordo em parte** | Se os learners forem learners Raft de verdade, o Raft já prova o prefixo; o risco está no canal paralelo e na reconstrução do gate (decisão 3) |
| P0-3 e 3.3 DELETE e replay | **Concordo em parte** | Na reaplicação em ordem o resultado final é correto; o buraco está nos caminhos que fogem do checkpoint (rebuild, restauração) e na falta de prova (decisão 4) |
| P0-4 Rebuild sem protocolo | **Concordo** | Falta mesmo. Esbocei um protocolo (seção 3) |
| P0-5 SKIP de lote parcial | **Concordo** | O SKIP deve ser por item, não por entrada (decisão 5) |
| P0-6 RPO 0 condicional e header | **Concordo** | O header não é aceite de risco. Proponho política por instalação (decisão 2) |
| P0-7 Compatibilidade DynamoDB | **Concordo em parte** | Falta o inventário real de chamadas e uma suíte de conformidade; o recorte da API é uma decisão, não um defeito |
| P1 ReadIndex e apply | **Concordo** e acho um problema pior | A leitura forte fica acoplada à saúde da Azure (decisão 6) |
| P1 GSI "forte" | **Concordo** | Alinhar ao DynamoDB (decisão 7) |
| P1 `_dr_v` | **Concordo** | Filtrar nas respostas e testar contra o DynamoDB real |
| P1 Idempotência de 5 min | **Discordo em parte** | O DynamoDB também não é idempotente por operação de negócio; o defeito é a v1 excluir a escrita condicional (decisão 8) |
| P1 Backlog Cosmos | **Concordo** | Já é o R03; falta o limite de lag para elegibilidade ao failover |
| P1 NetworkPolicy sem autenticação | **Concordo** | O ADR-043 resolveu o cliente, não a autorização por aplicação (decisão 9) |
| P1 Seis clusters por cliente | **Concordo** | Precisa de estimativa de custo antes de qualquer outra conversa |
| P1 Atomicidade do lote | **Concordo** | Mesmo tema do SKIP por item |
| Eventual: ADR-009 contra ADR-023 | **Concordo e sou mais duro** | Retirar o apply paralelo da v1 (decisão 10) |
| Seção 5: DynamoDB por célula | **Concordo** | É o maior ganho de custo e simplicidade; novo ADR de trade-off (decisão 11) |
| Seção 6: código Go | **Concordo nos oito** | Corrigir só depois da sua decisão (decisão 12) |
| Seção 9: discordâncias de documentação | **Concordo nas seis** | Uma delas eu mesmo introduzi (decisão 13) |
| Seção 8: oportunidades | **Concordo** | Anti-entropia, conformidade e métricas de garantia entram no plano |
| Seção 10: fases | **Concordo** | Com um ajuste de ordem (decisão 14) |

## 2. Ponto a ponto

### 2.1 Fencing e split-brain (P0-1, 3.1)

O relatório acerta. E o desenho do relatório mostra algo que o pacote não diz com clareza: **o conflito nasce da opção B**. Com a opção B a AWS continua escrevendo quando o link cai. Se a Azure for promovida por uma pessoa nesse intervalo, as duas lados aceitam escrita. Revogar credenciais e checar a epoch no boot não impedem isso, porque o lado antigo não sabe que foi substituído.

Sem terceiro domínio (a restrição dura de só AWS e Azure descartou o desempate externo, ADR-008), a saída tecnicamente honesta é **tempo, não consenso**:

1. A entrada `DEGRADE` carrega uma **validade** (lease) de duração D, que é o teto de duração do RPO > 0 que o PEND-02 já precisa aprovar.
2. Ao fim de D, a AWS **para de aceitar escrita sozinha**. Só uma ação humana, replicada pelo Raft da AWS, prorroga.
3. A Azure só pode ser promovida **depois de D mais uma margem de relógio** contada desde o último contato com a AWS (relógio monotônico local).
4. Resultado: nunca há dois lados escrevendo, assumindo apenas deriva de relógio limitada (a mesma premissa de todo lease).

Custo e cenários ocultos:
- Se a AWS morre de verdade em modo degradado, o RTO passa a ser pelo menos D. É o preço de ter autoridade única sem terceiro domínio.
- Entre a partição e o `DEGRADE` (2 s) também há uma janela: promoção nesses 2 s precisa seguir a mesma regra de espera.
- Pausas longas de processo (GC, VM congelada) são o inimigo clássico de lease. A AWS precisa checar a validade **a cada ACK**, não só em um temporizador.
- Se o negócio não aceita nem D finito, a alternativa é abandonar o modo degradado (decisão 1b): a partição para as escritas, o RPO fica 0 sempre e a disponibilidade cai. É coerente com o requisito original de RPO = 0, e deveria ser dito ao negócio assim.

Sobre classificar a PEND-01 como bloqueio de segurança: **concordo**. Eu a tratava como assunto dos outros arquitetos; com a decisão 1 ela passa a ter uma proposta concreta para levar a eles.

### 2.2 Gate e identidade do prefixo (P0-2, 3.2)

Em parte. Se os learners da Azure são **learners do `etcd-io/raft`**, o líder só conta o progresso de um learner depois da verificação de consistência do `AppendEntries` (índice e termo anteriores). Isso é a propriedade de casamento de logs do Raft e já garante que "índice 100 durável" significa "o mesmo prefixo até 100". O risco aparece em dois lugares:

- No esboço, `LearnerDurable()` devolve apenas três números, sem termo. Se o progresso vier por um canal paralelo ao Raft, o número sozinho não prova nada.
- Na **reconstrução do gate pela líder nova** (ADR-029): ela "consulta os learners". Um learner pode ter um sufixo de uma líder antiga. A líder nova só pode contar um índice que ela mesma confirmou **no termo novo**; qualquer valor reportado fora de banda deve ser ignorado até a primeira confirmação.

Concordo em ampliar o TLA+ para P9 em diante: promoção entre nuvens, troca de membros (disco substituído, identidade nova), dedupe e operações com resultado desconhecido. E concordo em separar nos nomes e nas métricas `committed`, `azure_durable`, `acked` e `applied`.

### 2.3 DELETE, TTL e replay (P0-3, 3.3)

Em parte. Na reaplicação normal o applier reprocessa **em ordem por chave** a partir do checkpoint: `Put(10)` recria a chave e `Delete(11)` a remove de novo. O estado errado existe só durante o replay, e a regra "leituras só abrem depois do high-water mark" o esconde. Então não achei janela insegura **se essa regra for imposta por código e provada**.

Onde o relatório tem razão:
- Nada disso está provado. É uma invariante ("com leituras abertas, o estado materializado é a dobra do log") que deveria estar no TLA+ e em teste de propriedade.
- Os caminhos que **não passam pelo checkpoint** (rebuild, restauração PITR, cópia de outra célula) não têm a mesma proteção.
- O TTL local do cliente expira itens em cada célula de forma independente; um replay pode recriar um item já expirado até o próprio TTL apagá-lo de novo.

Sobre tombstones: o custo escondido é a **leitura**. Tombstone dentro do item exige filtrar em `Query`, `Scan` e GSI, o que muda o conteúdo das páginas com `Limit` e quebra a transparência. Uma alternativa mais barata é uma **tabela separada de tombstones consultada só em modo de replay ou rebuild** (índice abaixo do high-water mark anterior). Na escrita nova o índice é maior que tudo e a condição `v < índice` basta. O custo é uma escrita extra por DELETE.

### 2.4 Reconstrução sem protocolo (P0-4)

Concordo. Esboço do protocolo que falta (cópia difusa mais replay, que converge porque o apply é condicional por versão):

1. Registrar o índice **F** do log antes de começar.
2. Copiar para uma **tabela sombra** por varredura (não precisa de snapshot), gravando cada item com a condição de versão.
3. Reaplicar o log a partir de F até alcançar a cabeça. Como `Delete` e `Put` posteriores a F são reaplicados em ordem, a cópia converge.
4. Quando o lag estiver abaixo de um limite, pausar a escrita da célula, fechar o último trecho e **trocar o nome lógico** para a tabela sombra (cutover).
5. Verificar por checksum por faixa de chaves (anti-entropia) antes de religar as leituras.

Cenários ocultos: o log precisa estar retido desde F (se a varredura demorar mais que a retenção do teto de 70%, o rebuild falha); o custo de RU do Cosmos durante a varredura; e o Cosmos precisa do mesmo protocolo pelo Cloud-Adapter.

### 2.5 SKIP de lote parcial (P0-5)

Concordo, e o que escrevi no C26 está discutível: "pular a entrada pula o lote inteiro, inclusive os itens bons". O DynamoDB já admite sucesso parcial em `BatchWriteItem`. Como o apply é determinístico, todas as células aplicam os mesmos 10 itens e param no 11º, então **não há divergência entre réplicas**; há uma semântica ruim. Proposta: o SKIP identifica **(índice, ordem do item)** e o applier grava o progresso por item. Replay reaplica os dez primeiros de forma idempotente e pula só o 11º.

### 2.6 RPO 0 condicional e header (P0-6)

Concordo. Como o ADR-043 deixou cada cliente com a sua instalação, a política pode ser uma **variável obrigatória por instalação**, sem valor padrão: `DEGRADED_POLICY=accept` (com o lease da decisão 1) ou `reject` (as escritas falham durante a degradação, RPO 0 sempre). O cliente de dados monetários escolhe `reject` e o header deixa de ser o único aviso.

### 2.7 Compatibilidade DynamoDB (P0-7) e `_dr_v` (P1)

Em parte. O recorte da API (sem `UpdateItem` com expressão, sem condicional, sem transação) foi decisão sua, com os trade-offs registrados no ADR-025; não é defeito. O que falta, e o relatório está certo, é:
- o **inventário real de chamadas e parâmetros** dos clientes (por exemplo a partir de CloudTrail de eventos de dados ou logs do SDK) antes de prometer "só trocar o endpoint";
- uma **suíte de conformidade** que roda a mesma sequência contra o DynamoDB real, contra a camada e contra o Cosmos pelo Cloud-Adapter, comparando resultados;
- a regra de **remover `_dr_v` de todas as respostas** (`GetItem`, `Query`, `Scan`, projeções), com teste. Hoje o R17 só trata o limite de 400 KB.

### 2.8 Leitura forte e apply (P1) e um problema que o relatório não vê por inteiro

O relatório aponta que uma entrada pendente bloqueia leituras fortes de todo o shard. Concordo. Há algo pior no desenho atual: no modo Strong o apply só acontece **depois do gate**, e a leitura forte espera `appliedIndex >= ReadIndex` (índice commitado pelo Raft). Logo, **uma Azure lenta atrasa as leituras fortes**, mesmo sem nenhuma escrita à espera. Isso acopla a disponibilidade de leitura à saúde do link, no exato cenário da faixa cinzenta (C25).

Correção simples: um cliente só pode ter visto escritas já ACKadas, e toda escrita ACKada está abaixo do gate. A barreira da leitura forte pode ser **`min(ReadIndex, gate)`**. Isso desacopla a leitura da Azure e mantém a leitura-do-que-escrevi.

### 2.9 GSI (P1)

Concordo. O DynamoDB só oferece leitura eventual em GSI. O ADR-024 diz "leitura forte em GSI até propagação do índice", o que sugere uma garantia que a camada não tem como dar. A regra simples é a do próprio DynamoDB: `ConsistentRead=true` em GSI é recusada. Há ainda uma assimetria depois do failover: o índice do Cosmos é consistente por padrão, o do DynamoDB não, e a aplicação não deve depender de nenhum dos dois.

### 2.10 Idempotência (P1)

Em parte. Um `PutItem` cego repetido depois de 6 minutos sobrescreve uma versão mais nova **também no DynamoDB**; a camada não piora isso além da janela de dedupe. O defeito é outro: **a v1 exclui escrita condicional**. Uma aplicação que precisa de concorrência otimista (versão, saldo) não tem como se proteger pela camada. Para dados monetários isso merece o levantamento do uso real de `ConditionExpression` pelos clientes. Um identificador durável de operação de negócio seria uma extensão fora do contrato DynamoDB.

### 2.11 Autenticação entre app e proxy (P1) e o ponto 5 das discordâncias

Concordo, e a falha é minha: o ADR-043 respondeu "quem é o cliente", não "quem pode escrever o quê". Hoje cada aplicação do cliente tem o seu papel IAM, normalmente limitado às suas tabelas. Pelo proxy, **o papel largo é o do `DataReplication`** e qualquer pod do namespace escreve em qualquer tabela. Ou seja, há **ampliação de privilégio** em relação ao cenário de hoje, não só impersonação. O `Access Key Id` do SigV4 não resolve, pois a camada não consegue validar a assinatura sem o segredo. A saída é identidade de carga de trabalho (mTLS por aplicação, certificado emitido pelo cert-manager) com uma lista de tabelas e operações permitidas por identidade.

### 2.12 Eventual (ADR-009 contra ADR-023)

Concordo, com mais ênfase. No Eventual o apply no banco da líder corre em paralelo com a espera da Azure. Um leitor pode ver uma escrita **que ainda não é durável na Azure** e que nunca será ACKada se a AWS cair antes. Quem leu agiu sobre um valor que depois "não aconteceu". O ganho é esconder `t_apply` (alguns milissegundos) atrás da Azure. Para esse ganho, acho o risco desproporcional na v1. Recomendo manter só a **leitura** eventual (o E1 original do ADR-009) e retirar o apply paralelo, reavaliando se a medição mostrar necessidade.

### 2.13 DynamoDB por célula não é DynamoDB por AZ (seção 5)

Concordo, e é o achado de maior retorno. O DynamoDB é regional e já replica por três AZs. Três tabelas na mesma região **não são três domínios de falha físicos**; o Cosmos com redundância de zona vale o mesmo raciocínio. O que as seis cópias de fato compram é **isolamento lógico** (corrupção ou throttling de uma tabela não atinge as outras, e o applier de uma célula não depende das demais). Esse é o argumento a pesar contra seis materializações, seis fluxos de reconciliação e os problemas de fencing, rebuild e custo (R03, R14, R20).

A alternativa com **um banco regional por nuvem** (duas materializações) mantém os três logs Raft por AZ, que é onde a durabilidade por AZ realmente existe. O applier passa a ser da líder, e a troca de líder continua segura porque o apply é idempotente por versão. Riscos a pesar: um só ponto lógico de corrupção na AWS (a Azure continua sendo a cópia), throttling compartilhado entre escrita ao vivo e replay, e o fato de a arquitetura celular da organização poder **exigir banco por célula** como padrão de bulkhead. Pergunta que só você responde: isso é regra da organização ou escolha nossa?

### 2.14 Código Go (seção 6)

Conferi no `codigo_exemplo`. Todos procedem:

| Arquivo | Confirmado |
|---|---|
| `gate.go` | `WaitFor` não remove o waiter quando o contexto expira; `SetAzureDurable` guarda só o número, sem termo |
| `replicate.go` | O gate só consulta `LearnerDurable()` ao processar um lote commitado; sem novo commit, a escrita espera até o timeout. `b.Entries[len(b.Entries)-1]` quebra com lote vazio e o fechamento do canal não é tratado. `Log.Append` é síncrono (o comentário do esboço já admitia) |
| `write.go` | Sem líder conhecida segue para `Propose`, em vez de esperar até 1 s e responder 503 (ADR-039) |
| `handler.go` | `WriteTimeout` de 5 s contra `REQUEST_DEADLINE` de 7,5 s; mensagem de erro concatenada na mão em JSON |
| `app.go` | `Degraded` sempre `false`: o header crítico nunca sai |

A correção certa para o gate é **orientada a evento**: o progresso dos learners alimenta o gate no momento em que chega, independentemente do fluxo de commits.

### 2.15 Discordâncias de documentação (seção 9)

1. **Automático contra humano.** Confirmado. A definição de pronto fala em recuperação sem humano (exceto PEND-01 e H-DR1), mas depois decidimos que promoção, `SKIP`, `drctl rebuild` e órfãs são humanos. Falta a matriz automático, assistido e manual.
2. **Strong por operação e recurso.** Concordo (GSI, `Scan`, `Query` na tabela base).
3. **Dedupe por requisição contra dedupe por item.** Confirmado: o ADR-035 diz requisição inteira e a tabela de structs diz `BatchService` com "dedupe por item". Fui eu que deixei a tabela desatualizada.
4. **Latência e R18.** O R18 diz que o RTO do cenário é a "soma dos P99 de etapas sequenciais, não o P99 da soma". A soma dos P99 é uma aproximação conservadora por etapa, mas o relatório tem razão: o contrato precisa de um **SLO ponta a ponta** (latência, erro e recuperação) **medido sobre a operação completa**, e o RTO deve ser a distribuição medida em exercícios de falha (mediana, P95, pior caso), não uma soma de percentis.
5. **Identidade da instalação contra identidade de quem executa.** Ver 2.11.
6. **Header.** Ver 2.6.

### 2.16 Oportunidades e fases (seções 8 e 10)

Concordo com todas as oportunidades. Em ordem de custo e retorno: suíte de conformidade com histórico (barata e esclarece o risco de compatibilidade), métricas `last_committed`, `last_azure_durable`, `last_acked`, `last_applied` e `last_reconciled`, anti-entropia por checksum, rebuild com tabela sombra (seção 2.4), controle de admissão explícito e SLO de elegibilidade ao failover. A ressalva do relatório (checksum detecta, não previne) está correta.

Sobre as fases: concordo com a lógica. Ajuste de ordem: o **modelo TLA+ ampliado** (fase 2) deve nascer junto com a decisão da fase 1, porque o lease da decisão 1 só vale se for modelado. E o núcleo mínimo verificável deve vir **antes** de qualquer caminho de banco.

## 3. O que o relatório não pegou

1. **Leitura forte acoplada à Azure** (seção 2.8), com a correção `min(ReadIndex, gate)`.
2. **Ampliação de privilégio** pelo proxy (seção 2.11): não é só impersonação, o papel largo passa a ser o do `DataReplication`.
3. **Semântica do Cosmos depois do failover.** Na Azure a aplicação passa a falar com o Cosmos por tradução: tamanho de item, precisão de `Number` (PEND-08), índices consistentes e TTL são diferentes. A suíte de conformidade precisa cobrir o Cloud-Adapter, não só o DynamoDB.
4. **Relógio no lease.** O lease depende de relógio monotônico local e de checar a validade a cada ACK; pausas de processo são o caso perigoso.
5. **Escrita condicional excluída** (seção 2.10): para aplicações financeiras isso pode pesar mais que qualquer limite de latência.

## 4. Decisões (responda no formato `1a 2b ...`)

| # | Decisão | a | b | c | Rec. |
|---|---|---|---|---|---|
| 1 | Autoridade única na partição e promoção da Azure | Modo degradado com **lease** de duração D; a AWS para sozinha ao fim; a Azure só promove depois de D mais margem | **Sem modo degradado**: a partição para as escritas (RPO 0 sempre) | Como está: promoção humana e revogação de credenciais | **a**, ou b se o negócio não aceitar D finito |
| 2 | Política em modo degradado | `DEGRADED_POLICY` obrigatória por instalação (`accept` com lease ou `reject`) | Igual para todos | n/a | **a** |
| 3 | Gate e prefixo | Gate só conta progresso validado pelo Raft no termo atual; a líder nova ignora valores fora de banda; TLA+ ampliado (promoção, membros, dedupe, resultado desconhecido) | Como está | n/a | **a** |
| 4 | DELETE e replay | Manter DELETE real, com invariante formal e rebuild em tabela sombra | Tabela separada de tombstones, consultada só em replay e rebuild | Tombstone no item, filtrado nas leituras | **a** |
| 5 | SKIP de lote | Por item `(índice, ordem)`, com progresso persistido por item | Por entrada inteira, como hoje | n/a | **a** |
| 6 | Barreira da leitura forte | `min(ReadIndex, gate)` | Como está | n/a | **a** |
| 7 | Leitura forte em GSI | Recusar `ConsistentRead` em GSI, como o DynamoDB | Manter "forte até propagar" | n/a | **a** |
| 8 | Idempotência e condicional | Documentar o limite e levantar o uso real de escrita condicional nos clientes | ID durável de operação de negócio como extensão da API | n/a | **a** |
| 9 | Autorização por aplicação | mTLS por aplicação com lista de tabelas e operações por identidade | NetworkPolicy do namespace como fronteira | n/a | **a**, se as apps do namespace têm permissões diferentes hoje |
| 10 | Modo Eventual de escrita | Retirar o apply paralelo da v1; Eventual é só leitura | Manter, opt-in por instalação, com contrato "pode ler escrita não confirmada" | n/a | **a** |
| 11 | Banco por célula | Manter seis | Um banco regional por nuvem (duas materializações) | Novo ADR de trade-off com estimativa de custo antes de decidir | **c** (e antes: o banco por célula é regra da organização ou escolha nossa?) |
| 12 | Esboço de código | Corrigir os oito defeitos e escrever testes de concorrência | Deixar como está | n/a | **a** |
| 13 | Documentação | Corrigir as inconsistências (matriz automático/assistido/manual, dedupe por requisição, definição de pronto, SLO ponta a ponta, RTO como distribuição medida) e registrar os P0 como riscos bloqueantes | Só registrar os riscos | n/a | **a** |
| 14 | Ordem de trabalho | Cinco fases do relatório, com TLA+ ampliado e núcleo mínimo verificável antes dos seis caminhos de banco | Manter a ordem do guia de implementação | n/a | **a** |

**Ações sem decisão (já recomendadas):** protocolo de rebuild em tabela sombra (seção 2.4) como cenário e ADR novos; inventário real de chamadas dos clientes; suíte de conformidade contra DynamoDB, camada e Cloud-Adapter; limite de lag de apply do Cosmos para elegibilidade ao failover; métricas de garantia por shard.

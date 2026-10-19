# Compactação do log do Raft: decisões (do zero)

Início: 18/10/2026, thread "Compactação do log: grill". Desconsidera as ADRs antigas (063, 041, 047, PEND-24) a pedido do Bruno; elas serão revistas depois à luz deste arquivo.
Problema: [poc_log_sem_compactacao_oom.md](../explicacoes/poc_log_sem_compactacao_oom.md).
Objetivo: RAM, disco e tempo de partida limitados, sem quebrar RPO=0 e sem réplica atrasada divergir.

## Rodada 1 (Bruno, 18/10 16:56: 1b 2b 3b 4a 5a)

| # | Decisão |
|---|---|
| D1 | Uma réplica atrasada (Azure ou célula AWS parada) segura a compactação, mas só até um orçamento de tempo e bytes; passou, ela se reconstrói. |
| D2 | Orçamento-alvo: até ~1 h de atraso recuperável pelo log (~15 GiB a 3000 TPS e ~1,5 KiB por entrada). |
| D3 | Cauda recente em RAM; o restante retido fica em disco e é lido fora do laço do Raft. |
| D4 | Snapshot = marcador (índice, termo, membros e o estado pequeno derivado do log). O dado fica no banco da célula. |
| D5 | Estourou o orçamento: a réplica atrasada sai da janela e vai para reconstrução; as escritas continuam. |

- D3 impede `raft.MemoryStorage` com tudo dentro: é preciso um armazenamento com índice por posição ou um canal de catch-up sequencial.

## Mudança de premissa (Bruno, 18/10 17:00)

Não existe mais AWS primária e Azure failover. O grupo é um quórum homogêneo de N = 3, 5 ou no máximo 7 células, cada uma em qualquer provedor; o provedor só muda a latência. A solução deve ser agnóstica de provedor.

Como as decisões da rodada 1 se traduzem:
- "Réplica atrasada" (D1, D5) = qualquer célula fora da maioria que fecha o commit: lenta por latência ou parada. Não há modo especial: o Raft sempre confirma com a maioria, e D5 é o comportamento natural.
- O custo de retenção não cresce com N: o log retido é o mesmo, segurado pela célula mais atrasada dentro do orçamento. O que cresce com N é o tráfego de recuperação.
- A pergunta 6 da rodada 2 (modo sem a Azure) deixou de existir.
- Ponto aberto crítico: com 2 provedores, um deles sempre tem a maioria das células. Perder esse provedor inteiro tira o quórum, e as últimas escritas confirmadas podem existir só nele. RPO=0 contra perda de provedor depende do posicionamento (rodada 2).

## Rodada 2a (Bruno, 18/10 17:11)

| # | Decisão |
|---|---|
| DA | RPO=0 na perda de uma célula/AZ **e** na perda de um provedor inteiro. |
| DB | Em aberto: Bruno não entendeu as opções; reexplicadas com exemplo. |
| DC | Qualquer célula pode ser líder, com preferência pela que fecha a maioria mais rápido (transferência quando a latência muda). |
| DD | N fixo por instalação; uma célula nova entra por novo deploy via IaC, **sem reiniciar** os pods existentes. |

Contradição em DD, levantada na thread: entrar uma célula com as outras rodando é troca de membros em operação. Se cada célula tira a lista de membros da própria configuração, as antigas contam maioria de 3 e as novas de 5, e duas maiorias disjuntas podem eleger duas líderes. Exige ConfChange pelo log do Raft (célula nova entra como não votante, alcança, depois vira votante). A configuração só serve para a partida do grupo. Pergunta reformulada na rodada 2c.

## Rodada 2c

| # | Decisão |
|---|---|
| DD' | (Bruno, 18/10 17:19) A configuração do deploy só cria o grupo na primeira vez. Depois, a entrada de uma célula é um comando de administração que passa pelo log do Raft (ConfChange). A célula nova entra sem voto, copia o banco e alcança o log pelo canal de recuperação (perguntas 7 e 8), e só então vira votante. Os pods existentes não reiniciam. Subir N de 3 para 5 é feito uma célula por vez; com N=4 a maioria é 3 e o grupo tolera só uma falha. |

Consequências: a lista de membros passa a ser estado do log (vai no snapshot-marcador, D4), não configuração; a checagem de partida da POC (`ErrMembership`, PEERS × WAL) precisa mudar para "configuração só vale no bootstrap". A célula nova é o caso extremo de réplica atrasada, então o caminho de entrada e o de reconstrução (D5) são o mesmo.

## Mudança de premissa 2 (Bruno, 18/10 17:22)

Caso de uso real: AWS primária, Azure failover usada só com a AWS totalmente fora; composições 2+1, 3+2, 3 AWS, 5 AWS, até 7, **sempre com a maioria na AWS**. Análise: [solucao_aws_primaria_azure_failover.md](solucao_aws_primaria_azure_failover.md). B fica respondida por essa análise.

## Rodada 3 (Bruno, 18/10 17:27: Ea Fa Gb Ha)

| # | Decisão |
|---|---|
| DE | Multicloud: confirmação = maioria do Raft **e** K células Azure com fsync. ACK e apply só até esse índice; o índice nunca recua, nem na troca de líder. Só AWS: maioria simples. |
| DF | K = 1. Na promoção, escolhe-se a célula Azure com o maior índice durável. |
| DG | Azure inteira fora: as escritas seguem automaticamente com RPO>0, sinalizado ao cliente. É só nesse modo que o orçamento de retenção (D1, D2, D5) importa. |
| DH | Célula Azure vota, mas não vira líder por eleição; só vira líder por comando via API, executado por automação ou operador, quando a AWS de fato caiu. |

Consequências levantadas na thread:
- DH na prática é a promoção: com a maioria na AWS, a Azure sozinha não tem quórum, então "virar líder" exige recriar o grupo só com as células Azure a partir da de maior índice.
- O etcd-raft não tem votante que nunca se candidata; a célula Azure precisa suprimir a candidatura (não avançar o relógio de eleição enquanto follower). Desvio do uso padrão da biblioteca, precisa de teste.
- **DG + promoção por automação = risco de duas líderes** no corte do link: a AWS vê a Azure fora e segue (DG); a Azure vê a AWS fora e a automação promove. Exige cerca de tempo (lease) ou só operador. Pergunta J'.

## Rodada 3b (Bruno, 18/10 17:33: J'a, Ib)

| # | Decisão |
|---|---|
| DI | Multicloud usa sempre 3 AWS + 2 Azure. A opção 2 AWS + 1 Azure sai. A maioria fica sempre na AWS. Só AWS: 3 ou 5 células. |
| DJ | A promoção da Azure pode ser feita por automação ou operador, protegida por uma licença: a AWS só segue sem a Azure enquanto a licença D vale; a Azure só é promovida depois de D+M sem notícia da AWS. |

Contradição entre DG e DJ, levantada na thread: a licença só impede duas líderes se a AWS **parar** de escrever quando D vencer. Se a AWS seguir sem a Azure indefinidamente (DG puro), a Azure promovida depois de D+M convive com uma AWS viva escrevendo (corte do link). Sem terceiro local não há como resolver isso só com a licença. Pergunta K.

## Rodada 3c (Bruno, 18/10 17:37): K resolvida sem licença

Comportamento definido: link AWS↔Azure cai → a AWS segue com a líder e escrevendo (RPO>0 aceito enquanto durar); a Azure nunca elege líder sozinha; só vira líder por comando via API; quando o link volta, a Azure sincroniza até alcançar a AWS. **Substitui DJ: não há licença D+M.**

Risco residual levantado: o comando de promoção dado com a AWS viva (link cortado, AWS acessível aos clientes) cria duas líderes. A proteção passa a ser de quem dá o comando. Pergunta L (cerca antes da promoção).

Ligação com a compactação: o atraso da Azure cresce com a duração do corte. Até o orçamento (~1 h, D2), ela sincroniza pelo log; acima disso, sai da janela e se reconstrói (D5).

## Rodada 4 (Bruno, 18/10 17:41: Lb 7b 8a 9b 10c 11a)

| # | Decisão |
|---|---|
| DL | O comando de promoção não exige cercar a AWS antes. **Risco aceito:** comando dado com a AWS viva (corte de link) gera duas líderes aceitando escritas; a história de uma delas será descartada. |
| D7 | Réplica atrasada recebe as entradas antigas por um canal de recuperação separado, fora do laço do Raft, lendo os segmentos do WAL em sequência de qualquer célula que as tenha; só entradas commitadas e confirmáveis; a réplica grava no banco e depois aceita um snapshot-marcador no índice alcançado. |
| D8 | Reconstrução: cópia do banco de uma célula AWS sã pelo adaptador dela até F, numa tabela sombra; depois aplica o log desde F e troca. **Substituída pela DC-11 (rodada 6):** a cópia sobrescreve no lugar, sem tabela sombra. |
| D9 | Se a cópia passar do orçamento, F continua segurando o log só em disco, com teto duro de disco e alerta. |
| D10 | Snapshot e compactação da RAM a cada 10 mil entradas ou 64 MiB, o que vier primeiro. A partida relê só desde o último snapshot. |
| D11 | `GOMEMLIMIT` + teto duro de bytes no cache em RAM; estourou, o cache descarta e o leitor vai ao disco. O processo não cai por memória do log. |

Derivações (sem pergunta nova):
- Limite de compactação da RAM: até o aplicado local (o apply nunca precisa de entrada compactada). Limite de compactação do disco: `min(aplicado local, menor índice durável das réplicas dentro da janela, F − 1 de uma reconstrução em curso)`, limitado pelo orçamento D2.
- O WAL do etcd continua sendo o armazenamento: `wal.Open` a partir do último snapshot faz a partida reler só a cauda; os segmentos anteriores ficam só para o canal de recuperação até o limite de disco liberar (`ReleaseLockTo`).
- O progresso do applier é gravado com fsync antes de cada snapshot.

## Rodada 5 (Bruno, 18/10 17:44: Ma Na Ob)

| # | Decisão |
|---|---|
| DM | Snapshot = índice, termo, membros, índice confirmado (regra da Azure; nunca recua após reinício), identidade do grupo e da história. O resto fica no banco ou é recalculado. |
| DN | Reconstrução que bate no teto duro de disco é abortada; a réplica fica fora e tenta depois; as escritas seguem. |
| DO | A POC implementa tudo: snapshot-marcador, compactação da RAM e do disco, partida pela cauda, `GOMEMLIMIT`, teto do cache, canal de recuperação e reconstrução. |

Especificação consolidada: [especificacao.md](especificacao.md). Trecho da spec da POC: [spec_poc_trecho.md](spec_poc_trecho.md).

## Rodada 6 (Bruno, 18/10): canvas do SPDD e matriz de garantias

Origem: o canvas [GGQPA-XXX-202610181712](../../spdd/prompt/GGQPA-XXX-202610181712-[Feat]-service-compactacao-log-raft-recuperacao-reconstrucao.md) resolveu as lacunas da análise com as decisões DC-01 a DC-10; a [matriz de garantias](../../spdd/analysis/GGQPA-XXX-202610181805-[Analysis]-matriz-garantias-replicacao-rpo-disponibilidade-recuperacao.md) levantou as perguntas P-01 a P-05.

| # | Decisão |
|---|---|
| DC-01 | Duas entregas. A (memória, disco e partida) já trata o `MsgSnap` sem erro fatal; B traz a recuperação e a reconstrução. |
| DC-02 | Na follower, gate = `min(max(commit local, recuperado), recebido)`. O recuperado é o índice confirmado provado pela fonte do canal de recuperação. |
| DC-03 | O `CommitPump` é o único alimentador do applier; em recuperação ele lê da fonte remota. |
| DC-04 | A reconstrução termina com progresso F (fsync) e reinício do processo. Uma célula com progresso atrás do disco retido entra sozinha em reconstrução, em vez de não subir. |
| DC-05 | S = último índice entregue pelo laço **e** avançado (`Advance`), não o commit publicado. |
| DC-06 | O marcador (`cluster_id`, `epoch`, confirmado) é gravado num arquivo `snapshot` próprio com CRC e fsync, porque o snapshot do WAL do etcd não tem campo de dados. |
| DC-07 | Tamanho do segmento do WAL e teto duro absoluto configuráveis (`WAL_SEGMENT_BYTES`, `RETAIN_DISK_HARD_BYTES`), para os testes no notebook. |
| DC-08 | A regra do `Match` na RAM da líder só vale para as followers dentro da janela. Os testes reiniciam a líder para forçar o `MsgSnap`. |
| DC-09 | Fila de saída por par limitada em bytes (`PEER_QUEUE_BYTES`). |
| DC-10 | O pino da reconstrução na fonte dura até o canal de recuperação da célula reconstruída começar, ou até `REBUILD_PIN_TTL` (5 min). |
| DC-11 | (P-02) A reconstrução **sobrescreve no lugar** a tabela que a célula já tem, pelo `PutItem` condicional por `_dr_v`. Tabela sombra e apagar e recriar ficam fora. Fonte com `cluster_id` ou `epoch` diferente: recusa. O DELETE futuro exige lápide (ver `parklot/adr-tombstone.md`). |
| DC-12 | (P-03) **Fail-fast do volume do Raft perdido:** uma célula com o volume vazio só cria o grupo quando ele está nascendo (token de partida gravado no volume e registrado por quem cria o grupo). Com sinal de grupo existente, ela não sobe. A devolução segura (remover o ID e adicionar um novo por ConfChange) é a feature `troca-de-pvc-em-cell-ativa`, em `parklot/adr-troca-de-pvc-em-cell-ativa.md`. |
| P-01 | DC-04, DC-05, DC-06, DC-08 e DC-10 aprovadas. |
| P-04 | Backup e restauração do banco e do WAL não são deste produto: ficam com outro escopo. Um backup do WAL nunca é restaurado numa célula de um grupo ativo. |
| P-05 | Ordem das entregas: A → B → ConfChange e troca de PVC → multicloud (a volta da AWS depois da promoção usa a ConfChange). |

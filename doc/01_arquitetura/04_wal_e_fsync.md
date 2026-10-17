# WAL, retenção, reconstrução e fsync

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 6. Como será o WAL

### 6.0 Revisão de 13/10: o log do Raft no WAL do etcd é o único log do nó ([ADR-019](../03_adr/ADR-019-wal-proprio-volume-dedicado.md), [ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md))

> **Decisão do Bruno (13/10).** O WAL próprio no caminho do ACK (W2) **saiu**. O **log do Raft** usa o pacote `wal` do etcd (W1), um por shard, com fsync antes do ACK, num **volume de bloco dedicado por nó**. Ele é o **único log do nó** e a única fonte das entradas commitadas; a retenção para a Azure atrasada fica nele. O Pebble foi avaliado e descartado. O teto de 70% (ADR-041) vale para o volume do Raft. A retenção e a compactação estão em **6.9**. As seções 6.2 a 6.3 ficam como registro da análise anterior; onde contradizem a 6.9, vale a 6.9.

### 6.2 Opções
| Opção | Pró | Contra |
|---|---|---|
| **W1. Pacote `wal` do etcd, um por shard** | Muito testado | Um fsync por shard; sem group commit entre shards; peso por shard |
| **W2. WAL próprio, segmentado e compartilhado** (recomendado em 12/10; substituído em 13/10 pelo W1, ver 6.0) | Um fsync serve vários shards; formato sob nosso controle (post-image e versão); poucas linhas de código | **Risco de bug de armazenamento é o maior risco de perda de dado do projeto** |
| **W3. Pebble/LSM** | Pronto | Escrita amplificada, memtable e cache pesam; log Raft é *append + truncar*, não é carga de LSM |
| **W4. bbolt (raft-boltdb)** | Simples | Lento para log; fsync por transação |

**Usar RocksDB (ou outro banco) no lugar do WAL em arquivos? (pergunta do Bruno, 12/10)** Avaliação:
- **Trocar o motor não elimina o fsync.** Para o ACK valer RPO = 0, o dado precisa estar em armazenamento estável antes da resposta. O RocksDB com durabilidade ligada grava o próprio WAL (um log em arquivos) e faz fsync. Sem fsync, não é durável. O gargalo é o dispositivo (6.8, F1), não o formato do arquivo.
- **Um LSM é a ferramenta errada para o log do Raft.** O log é *append*, leitura sequencial e truncamento de prefixo/sufixo. O LSM acrescenta memtable, compactação, amplificação de escrita e memória, o que vai contra o critério de leveza e piora a cauda de latência quando a compactação roda.
- **O que os sistemas citados fazem (pelo que conheço; verificar nas versões atuais):**
  - **YugabyteDB** usa uma versão modificada do RocksDB como motor dos **dados** (estado da máquina), mas o **log do Raft é um WAL próprio em segmentos**; o WAL do RocksDB fica desligado justamente porque o log do Raft já cumpre esse papel.
  - **TiKV** usou RocksDB também para o log do Raft e depois criou o **Raft Engine**, um motor de log por anexação feito sob medida, para reduzir a amplificação de escrita.
  - **CockroachDB** (Pebble) separa o motor do log do Raft do motor dos dados.
  - **Vitess** não é Raft: replica o binlog do MySQL (semi-síncrono) e usa um serviço de topologia (etcd/Consul/ZooKeeper) à parte. Não oferece um log de consenso reaproveitável.
  - A tendência é um **log segmentado dedicado para o Raft** e um banco LSM para o **estado**. Aqui o estado já está no DynamoDB/Cosmos.
- **Custos práticos em Go:** RocksDB exige CGO (quebra o build estático/*distroless* e traz uma memória que o GC do Go não gerencia). O Pebble é Go puro, mas mantém as desvantagens do LSM acima.
- **Onde um banco embutido poderia ajudar:** índice de dedupe ou progresso do applier. O índice de dedupe é derivado do log e cabe em memória (seção 8.1). O progresso do applier por item é um registro pequeno no **volume do Raft** (6.3.1, ADR-048), então também não precisa de banco embutido.

**Conclusão (12/10, substituída em 13/10 pela 6.0 e 6.9):** manter o WAL segmentado próprio (W2). O que reduz o fsync é o **meio de armazenamento** (volume dedicado e provisionado; o NVMe local foi descartado pelo Bruno), medido no spike S1.

**Recomendação de 12/10, substituída em 13/10 pela 6.0 (W1 para o log do Raft):** W2 **atrás de uma interface de armazenamento do log (5.5)**, para poder trocar por W1 se os testes de falha ou o spike mostrarem problema. Eu **não** recomendo W2 sem a bateria de testes da seção 13: o WAL próprio só é aceitável se o orçamento de teste for tão sério quanto o de código.

### 6.3 Desenho do WAL (W2) — substituído em 13/10 (ver 6.9)

**Arquivos**
- Diretório por nó: `segments/` com arquivos de tamanho fixo (ex.: 64 MiB), **pré-alocados** (`fallocate`) para evitar variação de latência por alocação no fsync.
- Nome do segmento = primeiro índice global dele (ordenação trivial).
- Índice por shard mantido **em memória** e reconstruído na partida varrendo os segmentos (ou a partir de um marcador leve).
- **Progresso do applier por item** em registro próprio no mesmo volume (6.3.1).

**Registro** (alinhado e com tamanho prefixado):
```
| len u32 | crc32c u32 | type u8 | shardId u32 | term u64 | index u64 | payload ... |
```
- `type`: ENTRY, HARDSTATE, SNAPSHOT_MARK, TRUNCATE, SEGMENT_HEADER.
- `payload` de ENTRY: `commandId`, hash de dedupe, tabela (**nome lógico**), chave(s), **post-image**, **versão** (= índice Raft), tipo (PUT/DELETE/BATCH). Cada item da entrada tem uma **ordem** (1 para entrada de um só item); o par `(índice, ordem)` identifica o item para o apply, o SKIP e a consulta de resultado ([ADR-048](../03_adr/ADR-048-skip-por-item.md)).
- CRC32C (aceleração por hardware) sobre o cabeçalho e o payload.
- Cabeçalho de segmento com **versão do formato** e UUID do nó (evita copiar segmento de outro nó por engano).

**Escrita e group commit**
1. Cada shard produz um `Ready`. Em vez de fazer fsync por shard, os shards de um mesmo ciclo colocam seus registros num **buffer único**.
2. Uma goroutine escritora faz **um `write` + um `fdatasync`** para o lote inteiro e então libera todos os shards (que enviam as respostas Raft).
3. **Sem janela fixa de espera (linger).** Agrupar o que chegou enquanto o fsync anterior estava em andamento (agrupamento natural, ver 6.8). Se o sistema está ocioso, grava na hora.
4. **Falha de fsync = erro fatal** (o processo encerra). Não se repete o fsync: depois de erro, o kernel pode ter descartado as páginas sujas e um segundo fsync "dá certo" sem que o dado esteja no disco.

**Recuperação na partida**
- Varrer desde o último marcador. O **último registro inválido do último segmento** (CRC errado ou truncado) é tratado como escrita incompleta: trunca ali. Isso é esperado após queda de energia.
- Registro inválido **no meio** (com registros válidos depois) indica **corrupção**, não queda. O nó **recusa subir** e inicia o procedimento de reconstrução (6.6). Nunca "consertar" silenciosamente.

**Compactação (retenção)**
- Um segmento só é apagado quando: (a) todos os shards nele aplicaram e (b) nenhum follower/learner **dentro da janela de tolerância** precisa dele.
- A janela de tolerância para a Azure atrasada é o ponto crítico (ver 6.5).

### 6.3.1 Progresso do applier por item (ADR-048)

O applier **persiste o progresso por item `(índice, ordem)` num registro pequeno com CRC, sem fsync, no volume do Raft (arquivo `progress` ao lado do diretório do WAL; na POC, `/var/lib/dr/raft/progress`), não no banco**. Perdê-lo só alonga o replay, que é idempotente pela versão. Assim:
- Um replay depois de queda reaplica os itens anteriores de forma idempotente (condição `v < índice`) e **para ou pula só o item marcado**, e não a entrada inteira.
- O `SKIP(index, ordem, motivo, autor)` é uma entrada replicada e auditada; a entrada original nunca é apagada. Pular a entrada inteira exige listar todas as ordens e continua sendo decisão humana ([ADR-038](../03_adr/ADR-038-skip-de-entrada-venenosa-sempre-humano.md)).
- A consulta por identificador da requisição devolve o resultado **por item**: `APPLIED`, `REJECTED` ou `PENDING`.
- O índice do item que trava o applier é exportado como `poison_entry_index` (métrica por shard e por item).
- O arquivo local do ponto mais alto aplicado antes da queda (high-water mark, usado para abrir as leituras só depois do replay, ver 6.4.1) passa a fazer parte do mesmo registro de progresso.

### 6.5 Retenção versus Azure atrasada

> A retenção para a Azure fica **no próprio log do Raft** (disco do WAL e memória do `MemoryStorage`), no volume do Raft. O log do Raft só é compactado até `RaftCompactLimit`, que segura o índice durável da maioria Azure enquanto ela estiver dentro da janela (6.9).

O disco do WAL enche quando a Azure fica muito tempo fora (já era a falha mais grave da revisão). Regras da v1:
- Retenção por **tamanho máximo** (70% do volume do Raft, medido pelo espaço retido) e por **tempo mínimo** (cobrir a janela de manutenção planejada).
- Alertas em 50% e 70%.
- Ao passar do limite: o `DataReplication` **para de reter para a Azure**, a Azure é marcada como "fora de janela", deixa de segurar a compactação e volta por **reconstrução em tabela sombra** (6.6) em vez de *catch-up* pelo log. Prefiro perder a elegibilidade da Azure a derrubar o disco da AWS.
- **Retenção desde F para o rebuild (ADR-047).** Uma reconstrução em tabela sombra registra o índice **F** do log antes de começar e o log precisa estar retido **desde F** até o cutover. Se a varredura demorar mais que a retenção permitida pelo teto de 70% ([ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)), o rebuild falha e recomeça com um F novo. O dimensionamento do disco ([C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)) precisa contar esse caso.
- **Memória.** O `MemoryStorage` do etcd-raft guarda em RAM tudo o que não foi compactado: num modo degradado longo a memória do nó cresce com o atraso da Azure até o teto. Mitigações em aberto: teto da janela também por bytes retidos em memória, ou um `raft.Storage` que lê as entradas antigas do WAL em disco. Medir no spike S1.

### 6.6 "Snapshot" neste sistema e reconstrução de réplica
No Raft comum, o snapshot é uma cópia da máquina de estados. Aqui o estado vive **no banco** (DynamoDB/Cosmos), não no `DataReplication`. Então:
- O "snapshot" do Raft é só um **marcador** (`appliedIndex`, term, configuração) que permite descartar o prefixo do log.
- Uma réplica atrasada **além do que o log retém** não consegue se alcançar pelo log. Ela precisa de uma **transferência de estado**: copiar os itens do banco de um par (varredura consistente em um índice conhecido) ou restaurar um backup/export e então aplicar o log a partir dali.
- **Protocolo de reconstrução em tabela sombra (ADR-047, [C33](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md)).** Cópia difusa mais replay do log, que converge porque o apply é condicional por versão:
  1. registrar o índice **F** do log e reter o log desde F (6.5);
  2. criar a tabela sombra (sem GSIs, ADR-050) e copiá-la por varredura, gravando cada item com a condição de versão;
  3. reaplicar o log a partir de F até a cabeça (o DELETE é reaplicado em ordem);
  4. com o lag abaixo do limite, pausar a escrita da célula, fechar o último trecho e fazer o **cutover** do nome lógico para a tabela sombra, com nova **geração** (ver 6.4.3);
  5. verificar por **checksum por faixa de chaves** antes de religar as leituras (`last_reconciled`).
- Custos: WCU/RU durante a varredura, no DynamoDB e no Cosmos (o Cosmos precisa do mesmo protocolo pelo Cloud-Adapter). A anti-entropia por checksum detecta divergência, não a previne.
- Para a v1 a execução é **assistida**: o `drctl rebuild` conduz os passos e uma pessoa aprova o cutover (matriz em 9.5). A automação completa segue como hipótese aberta (H-DR1, seção 15).

### 6.7 Entradas e tamanho
- Um `BatchWriteItem` vira **uma entrada** ([ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md), [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md)), com os itens numerados por ordem (6.3.1); o dedupe é **por requisição**, não por item; transações estão fora do escopo da v1.
- Limite de **4 MiB** por entrada (A4), configurável (decidido pelo Bruno, 12/10, que deixou a escolha comigo). Acima, a escrita é recusada com erro de validação, sem entrar no log. Um item do DynamoDB tem no máximo 400 KB, e um batch grande pode passar de 4 MiB; esses casos são recusados.

---

### 6.8 fsync: o gargalo real e o que fazer (foco definido pelo Bruno, 12/10)

**Por que é o gargalo.** A 3.000 TPS o disco e a CPU sobram em vazão; o que conta é a **latência de cada fsync**, porque nenhum ACK sai antes de ele terminar. E o fsync aparece em três lugares em paralelo: no líder, nos seguidores AWS (a maioria 2/3 precisa de pelo menos um seguidor) e nos learners Azure (2 de 3). A latência do commit é o **máximo** entre o caminho AWS e o caminho Azure, cada um limitado pelo fsync mais lento necessário e por um RTT. O P99 do commit é dominado pela cauda de fsync desses discos.

**Valores de referência (a medir; não são garantia):** volumes de nuvem de uso geral costumam ter fsync de ~1 a 2 ms no caso típico, com picos de vários ms; volumes de alta performance e NVMe local ficam bem abaixo disso. O que importa é o **P99 e P99.9 sob carga e ao longo de horas**, não o valor típico.

**Medidas, em ordem de custo-benefício:**
| # | Medida | Efeito | Observação |
|---|---|---|---|
| F1 | **Escolha do volume** do WAL: dedicado, com IOPS e vazão provisionados (sem créditos de burst) e sem outro uso | Maior alavanca | Comparar: EBS gp3 e io2 (AWS), Azure Premium SSD v2 e Ultra Disk (Azure). NVMe local descartado pelo Bruno (12/10) |
| F2 | **Agrupamento natural** (group commit sem espera): enquanto um fsync está em andamento, os registros novos se acumulam e vão no próximo | Mantém a latência de 1 fsync por commit mesmo sob carga | **Decidido (ADR-056): por shard**, sem linger, teto de 1 MiB por lote. O `wal.Save` do etcd já faz um `fdatasync` por `Ready`, que traz o que chegou durante o fsync anterior. Entre shards só pelo gatilho do S1 (6.10) |
| F3 | **Líder grava em paralelo ao envio** aos seguidores | Tira o fsync do líder do caminho serial | **Decidido (ADR-056):** modo síncrono, só `MsgApp` da líder sai antes do fsync (o `doc.go` do `etcd-io/raft` permite, seção 10.2.1 da tese); `MsgAppResp` e votos só depois. O `AsyncStorageWrites` dá o mesmo ganho e entra só pelo critério do S1b |
| F4 | **Arquivos pré-alocados e `fdatasync`**, sem mudar o tamanho do arquivo | Evita gravar metadados do sistema de arquivos a cada fsync | Já previsto em 6.3 |
| F5 | **Escritas alinhadas ao bloco** (4 KiB) | Evita ler-modificar-gravar no dispositivo | Registros com preenchimento ou lotes alinhados; avaliar `O_DIRECT` + `O_DSYNC` no volume escolhido e comparar com `fdatasync` |
| F6 | **Sistema de arquivos e montagem**: XFS ou ext4 com `noatime`, WAL em volume só dele, sem journaling disputado por outros processos | Reduz a variação | Medir os dois |
| F7 | **Detectar disco lento** e agir | Protege a cauda | Medir o fsync de cada nó; acima do limiar por N segundos, a líder transfere a liderança (9.1) |
| F8 | **Dois de três (Azure) e AWS: usar o mais rápido** | Já faz parte do quórum | Mais réplicas rápidas melhoram o P99 do commit |

**Opção que eu não recomendo (decisão do Bruno, se quiser reavaliar): ACK com quórum em memória e fsync assíncrono.** Reduziria a latência, mas o dado ACKado só estaria em RAM nas réplicas. Quebra o RPO = 0 se várias réplicas falharem ao mesmo tempo. O cenário que preocupa não é perder uma AZ, e sim uma **falha de software correlacionada**: o mesmo binário rodando nas 6 réplicas, com uma requisição "venenosa" ou um bug que derruba todas ao mesmo tempo, antes do fsync. Isso perderia dado ACKado nas duas nuvens. Só considerar com uma janela de exposição limitada (fsync em até X ms) e aceite explícito do risco.

**Como funcionaria o ACK com quórum em memória (pergunta do Bruno, 12/10):**
1. O líder recebe a escrita, coloca no log em memória e começa a gravar no disco (fsync dele, em paralelo).
2. O líder envia a entrada aos seguidores AWS e aos learners Azure.
3. Cada réplica coloca a entrada em RAM e responde **"recebi"** na hora, sem esperar o próprio fsync, e grava no disco em segundo plano.
4. O líder avança um **índice de memória** quando há maioria AWS (2/3) e maioria Azure (2/3) com "recebi".
5. O applier aplica no banco e o ACK sai ao cliente. **Regra que não pode ser quebrada: o applier só aplica entradas que o fsync do líder já tornou duráveis.** Se aplicasse antes e o líder caísse sem a entrada no disco, o banco teria uma versão que o log não conhece, e a condição de versão do applier rejeitaria depois escritas legítimas.
6. Quando as réplicas terminam o fsync, avisam e o líder avança um **índice durável**. A diferença entre o índice de memória e o durável é a **janela de exposição**.
7. A janela precisa ter limite (por tempo e por número de entradas). Ao passar do limite, os ACKs esperam o disco (contrapressão).

**O que se ganha de verdade:** só o fsync dos **seguidores e learners** sai do caminho do ACK. O do líder continua (regra do passo 5). O ACK passa a esperar max(fsync do líder, RTT até as maiorias) em vez de max(fsync do líder, fsync das maiorias).

**O risco (por que eu não recomendo):**
- Uma réplica que respondeu "recebi" e cai antes do fsync **esquece uma entrada que já confirmou**. O Raft assume o contrário (estado persistido antes de responder). Exemplo: A1 e A2 confirmam em memória, as duas reiniciam juntas, A3, que não tinha a entrada, vira líder, e a entrada ACKada se perde.
- O gatilho realista é uma falha de software correlacionada (o mesmo binário nas 6 réplicas).
- O `etcd-io/raft` exige persistir antes de responder (contrato de `Ready`). Quebrá-lo tira a garantia provada da biblioteca, e o desenho precisaria de modelo em TLA+ próprio.

**E uma "memória durável" na frente, com o fsync em segundo plano? (ideia do Bruno, 12/10)** O conceito é bom: "durável" é uma questão de **contra o que** o dado sobrevive. Opções reais:

| Opção | Sobrevive a queda do **processo** | Sobrevive a queda do **host** (energia, kernel) | Disponível em AWS/Azure? | Custo/complexidade |
|---|---|---|---|---|
| **M1. Memória persistente de verdade** (NVDIMM, Optane PMem, RAM com bateria) | Sim | Sim | **Não** como oferta de VM comum (o Optane PMem foi descontinuado; verificar) | Alto |
| **M2. SSD NVMe local com proteção contra perda de energia (PLP)** | Sim | Sim (enquanto o nó existir) | **Sim** (instance store na AWS, discos locais na Azure) | Baixo: o fsync fica em dezenas a poucas centenas de µs, sem mudar a semântica |
| **M3. `write()` no arquivo do WAL e fsync assíncrono** (o cache de páginas do kernel segura o dado) | **Sim** (o kernel grava mesmo se o processo morrer) | **Não** | Sim | Baixo, mas é o ACK sem fsync com janela limitada |
| **M4. Segmento em memória compartilhada fora do processo** (`/dev/shm`, `emptyDir` em memória) | Sim (e sobrevive a reinício do contêiner no mesmo pod) | Não | Sim | Médio; M3 já cobre o mesmo caso com menos código |

**Avaliação:**
- **M3 e M4 atacam o cenário que mais me preocupava:** a falha de software correlacionada, em que o mesmo binário cai nas 6 réplicas. Como o dado fica no kernel (ou em memória compartilhada), o processo reinicia e recupera a cauda. O que sobra de risco é a **queda simultânea de vários hosts** dentro da janela (poucos ms), em máquinas e nuvens independentes, o que é bem menos provável do que o bug correlacionado.
- **Ainda é o ACK antes do fsync.** O Raft assume persistência antes da resposta. Com M3/M4 a "persistência" passa a significar "sobrevive a queda do processo", e o desenho precisa de modelo em TLA+ com esse modelo de falha. A regra do applier (só aplicar entrada já durável no líder) continua valendo.
- ~~**A recomendação é testar M2 primeiro.**~~ **Decisão do Bruno (12/10): o NVMe local não será testado.** O WAL fica em volume de bloco dedicado (EBS io2/gp3 na AWS; Azure Premium SSD v2 ou Ultra Disk na Azure). Sem M2, o piso do fsync é o do volume de rede, então o spike S1 é ainda mais importante para provar que o P99 cabe no orçamento.
- **M3 vira plano B**, com janela de exposição limitada e métrica `exposure_window`, e só com o aceite explícito do Bruno sobre o risco de queda simultânea de hosts.

**Teste de aceitação do fsync (spike S1, antes de congelar o desenho):**
1. `fio` com `fdatasync` por escrita (`--fdatasync=1`), profundidade 1, blocos de 4 a 64 KiB, por **várias horas**, em cada tipo de volume candidato nas duas nuvens.
2. Registrar P50, P99, P99.9 e o máximo. Repetir em horário de pico da conta/região.
3. Repetir com carga paralela (gravação do progresso do applier e compactação do WAL) no mesmo volume.
4. Critério de saída: o P99 de fsync cabe no orçamento de commit da seção 7, com folga para o RTT AWS↔Azure.

---

### 6.9 Retenção e compactação do log do Raft (13/10)

**Um único log.** O log do Raft (`wal` do etcd, um por shard, no volume do Raft) é a única fonte das entradas commitadas. O `CommitPump` lê as entradas commitadas por faixa da memória do Raft e as entrega ao Applier ([ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)); não há outro log nem escrita em dobro, e o único fsync no caminho do ACK é o do Raft (6.10).

**Progresso do applier.** O índice e a ordem da marca d'água (ADR-048, ADR-058) ficam num registro pequeno com CRC, sem fsync, no arquivo `progress` ao lado do diretório do WAL (na POC, `/var/lib/dr/raft/progress`). Perdê-lo só alonga o replay, que é idempotente pela versão.

**Regra de compactação** (`internal/domain/retention.go`)
1. **Segurança:** compactar o log do Raft só até `domain.RaftCompactLimit = min(aplicado local, índice durável da maioria Azure enquanto a Azure estiver dentro da janela, F − 1 de uma reconstrução em tabela sombra em curso)`. O aplicado local já está no banco da célula, então compactar até ele é seguro.
2. **Desempenho:** manter uma folga até o menor `match` dos votantes AWS, limitada por `RAFT_RETAIN_MAX` (proposta 64 MiB ou 30 s). Evita que um atraso curto de um seguidor o jogue no caminho de snapshot do `etcd-io/raft`, que pausa o envio para aquele par.
3. **Teto:** o espaço retido no volume do Raft não passa de 70% (ADR-041), com tempo mínimo que cubra a janela de dedupe (5 min). Acima do teto, a Azure sai da janela, deixa de segurar a compactação e volta por **reconstrução em tabela sombra** (ADR-047, 6.6).

Na POC a compactação está desligada (RT-11); o laço de compactação, o snapshot e o par que fica atrás dele estão no [ADR-063](../03_adr/ADR-063-snapshot-e-compactacao-do-log-do-raft.md), pendente.

**Catch-up.** A Azure atrasada, os learners e os seguidores AWS atrasados se alcançam pelo próprio Raft (`MsgApp`) enquanto o log do Raft retém o que eles precisam; o par grava com fsync no seu WAL antes de responder (5.5.2), e só então o progresso conta para o gate. Além do que o log retém, a volta é por reconstrução em tabela sombra. Não há canal de catch-up separado.

**Partida.** A retomada é o índice do progresso do applier (a entrada do progresso é reentregue inteira, e o applier pula os itens já gravados pela ordem salva); sem progresso, começa em 1. O Raft reinicia com `Applied = retomada − 1`, limitado entre o snapshot e o commit gravado, e o replay vem do WAL do Raft ([ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md), item 5). Se a retomada ficar antes do snapshot, o nó precisa de reconstrução.

**Memória.** O `MemoryStorage` guarda em RAM tudo o que não foi compactado; num modo degradado longo a memória do nó cresce com o atraso da Azure até o teto (6.5). Mitigações em aberto e medição no spike S1.

**Recuperação e falhas.** O `wal` do etcd valida o CRC na abertura e trata a cauda rasgada; erro de escrita ou de fsync é fatal (ADR-034). Perder o volume do Raft é disco perdido: o nó volta com identidade nova (ADR-034).

**Métricas:** `raft_log_bytes` (o espaço retido, que o teto mede), `raft_fsync_seconds` e o atraso do apply (`gate − aplicado`).

**Riscos:** em [02_riscos](../06_pendencias_e_riscos/02_riscos.md), entre eles o crescimento da memória do `MemoryStorage` num modo degradado longo.

---

### 6.10 Group commit do log do Raft e `AsyncStorageWrites` (ADR-056, 13/10)

> **Decisão do Bruno (13/10, respostas 1a 2a 3b 4a 5a 6a).** Análise completa, conferida no código do `etcd-io/raft` v3.6.0 e do `wal` do etcd v3.6.5: [analise_group_commit_e_async_storage_13_10](../06_pendencias_e_riscos/analise_group_commit_e_async_storage_13_10.md). Decisão: [ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md).

**Regra que não muda:** o buffer em memória é só a área de montagem do lote. Nenhum ACK, voto ou `MsgAppResp` sai antes do fsync que cobre a entrada. Continua valendo a decisão de 12/10: não existe ACK com quórum em memória.

**O que o `wal` do etcd já faz:**

| Item | Comportamento |
|---|---|
| `wal.Save(st, ents)` | Grava as entradas e o `HardState` e faz **um** `fdatasync` se houver entradas ou se mudou termo ou voto (`MustSync`) |
| Buffer | `PageWriter` que descarrega em fronteira de 4 KiB (F5) |
| Segmentos | 64 MB pré-alocados; o próximo é preparado em segundo plano (F4) |
| Entre chamadas | Cada `Save` faz o seu fsync: o lote é o que vem no `Ready` |
| Entre shards | Um WAL por shard, um fsync por shard |

**Laço do Ready por shard (adaptador `etcdraft`):**
1. Cada `Ready` traz as entradas novas e o `HardState`; o que chegou enquanto o fsync anterior rodava já vem junto no `Ready` seguinte. Sem espera fixa: com pouco tráfego, o lote é de uma entrada. Teto de lote: `RAFT_BATCH_MAX_BYTES` (1 MiB).
2. Um `Save` com `fdatasync` → `MemoryStorage.Append` → envia as respostas, na ordem.
3. Erro de escrita ou de fsync: o processo encerra **sem enviar nenhuma resposta** do lote (ADR-034).

**Modo da biblioteca:** síncrono com F3 na fase 1 (só `MsgApp` da líder sai antes do fsync; regra em `domain.CanSendBeforeSync`). O `AsyncStorageWrites` entra se o S1b mostrar redução de pelo menos 20% no P99 do commit ou eliminar eleições causadas por fsync lento.

**IOPS e o plano B entre shards.** Com agrupamento natural, um log faz cerca de `λ / (1 + λ·t)` fsyncs por segundo (`λ` = escritas por segundo naquele log, `t` = duração do fsync). A 3.000 TPS e fsync de 1,5 ms: ~545 fsyncs/s com um WAL compartilhado, ~1.900 com 8 shards, ~2.300 com 16. Por isso o volume do Raft tem IOPS provisionadas e há teto de shards por nó. **Gatilho do WAL compartilhado (W2):** no S1, na carga de 2x, fsync P99 acima do orçamento da seção 7 **ou** IOPS de escrita acima de 70% do provisionado. Se disparar, a volta do W2 é uma decisão nova, com a bateria completa de testes da seção 13.

**Contrapressão:** `MaxUncommittedEntriesSize` 32 MiB por shard e `MaxInflightBytes`; acima disso, `ThrottlingException` (os SDKs repetem; o dedupe de 5 min cobre). A líder também recusa antes do `Propose` quando o apply está atrasado demais (`gate − aplicado > APPLY_LAG_MAX`, [ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)), e o laço do Raft não espera mais o apply: o commit é publicado como sinal e o `CommitPump` lê por faixa.

**Métricas:** `raft_fsync_seconds`, `raft_fsync_per_second`, `raft_batch_entries`, `raft_batch_bytes`, `raft_unpersisted_bytes`, `raft_throttled_total`, `raft_elections_total`.

**Riscos:** 34, 35 e 36 em [02_riscos](../06_pendencias_e_riscos/02_riscos.md); pendência PEND-23.

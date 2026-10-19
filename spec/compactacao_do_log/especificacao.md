# Especificação: log do Raft com memória, disco e partida limitados

Versão 2, 18/10/2026 (rodada 6: DC-04 a DC-12). Origem: thread "Compactação do log: grill". Decisões e o porquê de cada uma: [decisoes.md](decisoes.md). Topologia: [solucao_aws_primaria_azure_failover.md](solucao_aws_primaria_azure_failover.md).

Esta especificação substitui o desenho anterior da compactação e da retenção (ver a seção 9). Ela vale para o produto. O que entra na POC está em [spec_poc_trecho.md](spec_poc_trecho.md).

## 1. Requisitos

| ID | Requisito |
|---|---|
| R1 | A memória do log é limitada por configuração, independentemente de carga, tempo no ar ou atraso de qualquer réplica. |
| R2 | O disco do log é limitado por um orçamento e por um teto duro; nenhuma réplica atrasada derruba o disco das outras. |
| R3 | A partida relê só a cauda do log (desde o último snapshot), não o histórico inteiro. |
| R4 | Nenhum ACK é perdido: RPO=0 contra a perda de uma célula/AZ e, no multicloud, contra a perda da AWS inteira. |
| R5 | Uma réplica atrasada nunca tem o índice do Raft à frente do que está no seu banco. |
| R6 | O laço do Raft nunca lê disco nem espera o apply. |

## 2. Topologia e regra de confirmação

| Instalação | Células | Confirmação |
|---|---|---|
| Só AWS | 3 ou 5 | Maioria do Raft |
| Multicloud | 3 AWS + 2 Azure (até 7, sempre com a maioria na AWS) | Maioria do Raft **e** pelo menos K=1 célula Azure com fsync |

- O índice confirmado é `min(commit do Raft, maior índice durável entre as células Azure)`, para K=1. ACK e apply só andam até ele, e ele nunca recua, nem na troca de líder nem depois de um reinício (ele vai no snapshot, seção 3).
- As células Azure votam, mas não se candidatam: enquanto follower, não avançam o relógio de eleição. Só viram líder por comando de promoção.
- Azure inteira inalcançável: as escritas seguem automaticamente na AWS, com RPO>0 sinalizado ao cliente. Quando o link volta, a Azure alcança pelo log (seção 5) ou, fora da janela, por reconstrução (seção 6).
- Promoção (AWS fora): comando via API, por automação ou operador; escolhe a célula Azure com o maior índice durável e recria o grupo só com as células Azure. **Risco aceito:** o comando dado com a AWS viva gera duas líderes, e a história de uma delas será descartada.
- Membros: a configuração do deploy só cria o grupo. Depois, entrar ou sair é comando de administração pelo log do Raft (ConfChange): a célula nova entra sem voto, passa pela reconstrução (seção 6), alcança e só então vira votante; uma de cada vez.

## 3. Snapshot (marcador)

O snapshot não carrega dados: o estado vive no banco de cada célula.

| Campo | Onde |
|---|---|
| Índice S, termo, membros (`ConfState`) | Metadados do snapshot do Raft (e o registro de snapshot do WAL) |
| Versão do registro, CRC | `Data` do snapshot do Raft e arquivo `snapshot` |
| Identidade do grupo (`cluster_id`) e da história (`epoch`) | `Data` do snapshot do Raft e arquivo `snapshot` |
| Índice confirmado no momento do snapshot | `Data` do snapshot do Raft e arquivo `snapshot` |

O registro de snapshot do WAL do etcd só guarda índice, termo e `ConfState`. Por isso o `Data` é gravado de forma durável num arquivo `snapshot` próprio, com CRC e fsync (tmp, fsync, rename), e relido na partida (DC-06). No `MsgSnap`, o `Data` vai no snapshot do Raft.

Regras:
- **Gatilho:** a cada 10.000 entradas ou 64 MiB desde o último snapshot, o que vier primeiro (`SNAPSHOT_EVERY_ENTRIES`, `SNAPSHOT_EVERY_BYTES`).
- **Índice:** S = último índice commitado já entregue pelo laço **e avançado** (`Advance`, o `applied` do Raft); não o último commit publicado, que o etcd-raft ainda pode precisar (DC-05). S não depende do apply no banco: a falta de dados no banco é tratada pela retenção em disco (seção 4) e pela regra de aceitação (seção 5).
- **Ordem:** (1) grava o progresso do applier com fsync; (2) grava o arquivo `snapshot`; (3) `wal.SaveSnapshot` (fsync); (4) `MemoryStorage.CreateSnapshot(S)`; (5) compacta a RAM (seção 4); (6) libera o disco (seção 4). Uma queda entre os passos só deixa um marcador mais antigo como ponto de partida.
- Roda numa goroutine própria, nunca no laço. O `wal` do etcd serializa `SaveSnapshot` e `Save` com o próprio mutex, então cada snapshot custa ao laço no máximo um fsync extra de espera. Isso precisa ser medido (`dr_raft_loop_duration_seconds`).

## 4. Retenção: RAM e disco

### RAM

- Compacta o `MemoryStorage` até `S − RAFT_CATCHUP_ENTRIES` (padrão 5.000), para que uma follower poucos milissegundos atrás não precise do canal de recuperação.
- Na líder, também não compacta abaixo do menor `Match` das followers **dentro da janela** (DC-08); uma follower fora da janela não segura a RAM.
- **Teto duro:** se os bytes no `MemoryStorage` passarem de `RAFT_RAM_MAX`, compacta até S, mesmo com followers atrás.
- `GOMEMLIMIT` em ~80% do limite do container.
- O que fica na RAM em regime: a cauda desde o último snapshot (até 64 MiB ou 10.000 entradas), mais as entradas não commitadas (`MaxUncommittedEntriesSize`, 32 MiB). O atraso do apply **não** fica na RAM: o leitor do log vai ao disco (seção 5).

### Disco

O WAL do etcd é o único armazenamento. Os segmentos `.wal` antigos ficam no disco até o **limite de disco** liberar:

```
limite_disco = min(
  aplicado no banco local (progresso com fsync),
  piso de retenção (menor índice durável das réplicas dentro da janela),
  pinos (leituras do canal de recuperação em curso, F − 1 de reconstrução em curso),
  S
)
```

- O piso de retenção é calculado pela líder (menor `Match` das réplicas na janela) e publicado às followers junto com o índice confirmado. Assim, uma follower que vira líder ainda tem o que as atrasadas precisam.
- **Janela:** uma réplica está na janela enquanto os bytes retidos desde o seu `Match` forem ≤ `RETAIN_MAX_BYTES` (orçamento; ~1 h de escrita, ~15 GiB a 3000 TPS). Passou, ela sai da janela e deixa de segurar o piso.
- **Teto duro** `RETAIN_DISK_HARD` (fração do volume): passou, os pinos de reconstrução são soltos (a reconstrução aborta e tenta depois) e as escritas seguem.
- Se o termo que segura o disco for o **aplicado local** (applier da própria célula parado), a célula não apaga o que o banco dela ainda não tem. No teto duro ela se declara fora (`/readyz` falso) e volta por reconstrução. Num item venenoso, que para o applier em todas as células, a admissão por atraso já para as escritas antes disso.
- Bytes retidos: soma do tamanho dos arquivos `.wal` a partir do arquivo que contém o índice (o nome do arquivo traz o primeiro índice dele).
- Liberação: `wal.ReleaseLockTo(limite_disco)` e remoção dos arquivos sem trava cujo arquivo seguinte começa em índice ≤ `limite_disco`.

## 5. Leitor do log e canal de recuperação

### Leitor do log (`LogReader`)

Um único leitor de entradas commitadas, usado pelo `CommitPump` (apply local) e pelo canal de recuperação:
- Lê da RAM quando o índice ainda está no `MemoryStorage`; abaixo disso, lê os segmentos `.wal` em sequência com `wal.NewDecoder` (exportado no `go.etcd.io/etcd/server/v3` v3.7.2), sempre fora do laço.
- **Regra da entrada final:** o WAL pode ter uma versão antiga, não commitada, de um índice, sobrescrita depois por outra líder. O leitor guarda as entradas num buffer, substitui o buffer a partir de i quando reaparece um índice i, e só libera as entradas ≤ c ao ler um registro de `HardState` com `commit = c`. Assim só sai entrada commitada e definitiva.
- Enquanto lê, registra um pino no índice em que está (seção 4).

### Canal de recuperação

Para uma réplica cujo próximo índice já foi compactado da RAM da líder:
1. O Raft da líder quer enviar `MsgSnap`. O marcador é pequeno, então é enviado normalmente, e a líder reporta `SnapshotFinish` quando o transporte entrega.
2. A réplica **só dá `Step` no `MsgSnap` quando o seu aplicado ≥ índice do marcador**. Antes disso, descarta a mensagem e inicia a recuperação. A líder volta a sondar e reenvia o marcador a cada sondagem; é barato.
3. A réplica escolhe uma fonte, qualquer célula cujo primeiro índice retido em disco seja ≤ `aplicado + 1`, preferindo uma follower para poupar a líder. Ela pede por gRPC as entradas a partir de `aplicado + 1`, em lotes de até 1 MiB, só até o índice confirmado.
4. As entradas entram no applier pelo mesmo caminho do `CommitPump` (apply condicional pela versão, marca d'água, progresso).
5. Quando o aplicado alcança o marcador, a réplica dá `Step` no `MsgSnap`, grava o snapshot (`wal.SaveSnapshot` + `ApplySnapshot`) e segue pelo Raft normal.

Risco conhecido: a réplica que voltou assim não tem as entradas anteriores ao marcador no próprio WAL. Ela não serve de fonte para elas, e o piso publicado pela líder garante que outras células sirvam.

## 6. Reconstrução (réplica fora da janela, ou célula nova)

Quando nenhuma célula retém `aplicado + 1`:
1. A réplica fica fora (`/readyz` falso, não responde leitura forte).
2. A réplica confere a identidade da fonte: `cluster_id` e `epoch` iguais aos do seu marcador. Diferente: recusa, sem gravar nada (DC-11).
3. A fonte (uma célula sã) fixa `F = aplicado no banco da fonte` e registra o pino `F − 1` (seção 4). O pino dura até o canal de recuperação da réplica começar, ou até `REBUILD_PIN_TTL` (DC-10).
4. A fonte envia as tabelas por Scan paginado pelo próprio adaptador de banco. A réplica grava **na tabela que já tem**, com a versão (`_dr_v`) original e a mesma condição do apply (`_dr_v < :v`). Não apaga nem recria tabela, e não usa tabela sombra (DC-11). Um item só anda para frente: um item velho é sobrescrito, um igual não muda e um alterado depois de F chega com versão maior, que o apply do log não regride.
5. Ao terminar a cópia: progresso = F com fsync, reinício do processo (DC-04) e o canal de recuperação a partir de F + 1. Só então `/readyz` verdadeiro.
6. Bateu no teto duro de disco: a reconstrução aborta, o pino é solto e ela tenta de novo depois, sobre a tabela que ficou.

Limites conhecidos:
- **DELETE real** quebra o passo 4: com a sobrescrita no lugar, um item apagado na fonte continua vivo na tabela da réplica. Quando o DELETE entrar, a lápide com `_dr_v` é obrigatória ([parklot/adr-tombstone.md](../../parklot/adr-tombstone.md)).
- Uma tabela de outra história (`epoch` diferente) não pode ser sobrescrita; nesta versão não há outro caminho, e a reconstrução é recusada.
- Se a cópia for mais lenta que a taxa de escrita, o pino segura o disco até o teto duro, e a reconstrução nunca termina. A vazão da reconstrução precisa ser medida.

## 7. Partida

- `wal.Open` a partir do último snapshot S: o `ReadAll` só traz as entradas depois de S para a RAM.
- O applier retoma do progresso P. Se P < S, as entradas de P+1 a S vêm do disco pelo leitor do log. O limite de disco ≤ aplicado local garante que elas ainda estão lá.
- Se o progresso estiver ausente, corrompido ou atrás do disco retido, a célula sobe direto em reconstrução (DC-04).
- **Volume do Raft vazio (DC-12):** antes de criar o WAL, a célula grava um token de partida no volume e pergunta aos pares se o grupo existe. Só cria o grupo se todos os pares estão vazios, ou se os que já rodam registraram o token dela no nascimento (`Genesis`), e se a tabela local está vazia. Com um par sem resposta, espera. Com sinal de grupo existente, não sobe. Devolver essa célula é a feature `troca-de-pvc-em-cell-ativa` ([parklot/adr-troca-de-pvc-em-cell-ativa.md](../../parklot/adr-troca-de-pvc-em-cell-ativa.md)).
- Backup e restauração do banco e do WAL ficam fora deste produto (P-04). Restaurar um backup do WAL numa célula de um grupo ativo equivale a perder o volume, e o fail-fast não detecta esse caso.

## 8. Garantias (o que não pode ser quebrado)

| ID | Garantia |
|---|---|
| G1 | O disco nunca é liberado além do aplicado no banco local, com fsync do progresso antes. |
| G2 | Uma réplica nunca dá `Step` num `MsgSnap` com o aplicado menor que o índice do marcador. |
| G3 | O leitor do log só entrega entradas confirmadas como definitivas por um `HardState` posterior. |
| G4 | O canal de recuperação só envia até o índice confirmado. |
| G5 | Nada de snapshot, compactação ou leitura de disco dentro do laço do Raft. |
| G6 | O índice confirmado nunca recua (no snapshot e na troca de líder). |
| G7 | Os bytes no `MemoryStorage` nunca passam de `RAFT_RAM_MAX` por mais de um ciclo do compactador. |
| G8 | A reconstrução nunca regride, apaga ou recria: só grava itens com a versão da fonte, pela condição do apply, e só com a mesma história (DC-11). |
| G9 | Uma célula com o volume do Raft vazio nunca cria um grupo que já existe (DC-12). |

## 9. O que esta especificação substitui (para revisar depois, sem apagar nada ainda)

| ADR / item | Situação |
|---|---|
| ADR-063 (snapshot e compactação) | Substituída por esta especificação. |
| ADR-041 (teto de 70%, sem S3) | Substituída: orçamento em bytes (`RETAIN_MAX_BYTES`) + teto duro; continua sem S3. |
| Risco 46 / PEND-24 (RAM no degradado longo) | Resolvidos: a retenção da Azure fica em disco; a RAM tem teto próprio. |
| ADR-047 (reconstrução em tabela sombra) | Muda (DC-11): mantém F e a versão, mas a cópia sobrescreve no lugar, sem sombra. O DELETE futuro exige lápide. |
| ADR-017 (Azure learners, gate 2/3 AWS + 2/3 Azure) | Muda: Azure votante e sem candidatura; gate = maioria + K=1 Azure. |
| ADR-014, ADR-044, ADR-045 (degradado, licença D+M, política) | Mudam: o degradado é automático e não há licença; o risco de duas líderes é aceito. |
| ADR-033 (promoção humana) | Muda: promoção por comando via API, por automação ou operador. |
| ADR-046 (gate monotônico) | Mantida (G6). |
| RT-10 da POC (membros fixos pelo `PEERS`) | Muda: a configuração só cria o grupo; membros por ConfChange. |

## 10. Hipóteses em aberto

- O tamanho médio da entrada (~1,5 KiB) e o orçamento de ~15 GiB, a medir com o soak (CF-06).
- A vazão da reconstrução por Scan e se ela converge sob 3000 TPS.
- A latência AWS↔Azure em São Paulo somada ao fsync da Azure em cada ACK.
- O custo do fsync do `SaveSnapshot` no laço (a cada 10.000 entradas).
- A supressão de candidatura nas células Azure é um desvio do uso padrão do etcd-raft e precisa de teste.

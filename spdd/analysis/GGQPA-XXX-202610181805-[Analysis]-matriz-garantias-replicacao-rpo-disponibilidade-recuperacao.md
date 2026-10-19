# Matriz de garantias: replicação, RPO=0, disponibilidade e recuperação

Data: 18/10/2026. Base: código atual do `DataReplicationApp`, [SPEC_POC_LOCAL.md](../../spec/SPEC_POC_LOCAL.md), [decisoes.md](../../spec/compactacao_do_log/decisoes.md), [spec_poc_trecho.md](../../spec/compactacao_do_log/spec_poc_trecho.md) e o canvas [GGQPA-XXX-202610181712](../prompt/GGQPA-XXX-202610181712-[Feat]-service-compactacao-log-raft-recuperacao-reconstrucao.md).

## Legenda

Estado:
- **POC hoje**: está no código e tem CA na spec. Os testes existem, mas não foram rodados nesta análise.
- **Canvas**: desenhado nas operações O-30 a O-44, ainda não implementado.
- **Fora (decidido)**: decidido em `decisoes.md`, mas fora da POC e do canvas.
- **Não discutido**: não aparece em nenhuma decisão nem na spec.

Fundamento: **[F]** fato do código ou do algoritmo; **[I]** inferência, sem medição.

## 1. Replicação e confirmação

| # | Garantia | Mecanismo | Onde | Prova | Estado | Lacuna |
|---|---|---|---|---|---|---|
| G-01 | Toda escrita confirmada está no log de todas as células que acompanham o grupo | etcd-raft v3.7.0. O WAL grava com fdatasync antes do `MsgAppResp`; só o `MsgApp` da líder sai antes do fsync | `etcdraft/node.go:175-192` [F] | CA-03, CA-04, CA-06 | POC hoje | — |
| G-02 | Sem quórum, não há ACK nem apply | O ACK espera o gate e depois o apply local. Na líder, o gate é o commit do Raft | `usecase/write.go:84`, `domain/gate.go` [F] | CA-07 | POC hoje | — |
| G-03 | Na follower, o apply nunca passa do que a líder provou | Gate da follower = `min(commit local, gate recebido)` (G-06) | `domain/gate.go:13,78` [F] | CA-15, CA-17 | POC hoje | Durante a recuperação o gate trava (o commit local não anda). O canvas resolve com o `SetRecovered` (DC-02) |
| G-04 | O replay é idempotente: reaplicar não muda o dado | `PutItem` condicional `attribute_not_exists(_dr_v) OR _dr_v < :v` | `dynamodb/store.go:85` [F] | CA-08 | POC hoje | — |
| G-05 | O apply segue a ordem do log por chave e nunca pula uma entrada | Workers por chave, marca d'água contígua, item venenoso para a marca | `usecase/apply.go` [F] | CA-15, CA-16 | POC hoje | — |
| G-06 | Leitura consistente | Barreira na líder antes do `Get`/`Scan` | `usecase/read.go:44-73` [F] | CA-05 | POC hoje | — |

## 2. RPO=0

| # | Garantia | Mecanismo | Onde | Prova | Estado | Lacuna |
|---|---|---|---|---|---|---|
| R-01 | RPO=0 na perda de 1 de 3 células | A maioria (2 de 3) tem fsync antes do ACK (G-01, G-02) | Código atual [F] | CA-07, CA-09 | POC hoje | O fsync dentro do Docker Desktop no macOS pode não chegar ao disco físico (cache do SSD e a VM) [I]. Na POC isso prova a lógica, não a durabilidade |
| R-02 | RPO=0 mesmo com a compactação ligada: nada que alguém precise é apagado | Disco nunca liberado além do aplicado local, com fsync do progresso antes (SG-18). `MsgSnap` nunca vai ao Raft com aplicado < marcador (SG-19). O `LogReader` só entrega entradas confirmadas por um `HardState` (SG-20). O `CatchUp` só envia até o gate da fonte (SG-21) | Canvas O-32 a O-36, O-38 | CA-20, CA-21, CA-22, CA-24 | Canvas | — |
| R-03 | O índice confirmado nunca recua depois de um reinício | `confirmado` no marcador com fsync, e `Gate.Restore` na partida (DM) | Canvas O-31, O-33 (DC-06) | CA-22 e teste unitário do marcador | Canvas | — |
| R-04 | RPO=0 na perda do provedor inteiro (multicloud) | Confirmação = maioria do Raft **e** K=1 célula Azure com fsync. ACK e apply só até esse índice, que não recua na troca de líder (DE, DF) | Nada no código. O gate atual só conhece o commit | Nenhuma CA | Fora (decidido) | É a maior lacuna para a sua pergunta. Exige mudar o gate na líder (K-ésimo índice durável da Azure), propagar esse índice e um teste de perda da AWS inteira |
| R-05 | Com a Azure inteira fora, as escritas seguem com RPO>0 avisado ao cliente | Modo sem Azure (DG) | Nada no código | Nenhuma CA | Fora (decidido) | Falta definir como o cliente é avisado (cabeçalho? código?) |

## 3. Alta disponibilidade

| # | Garantia | Mecanismo | Onde | Prova | Estado | Lacuna |
|---|---|---|---|---|---|---|
| H-01 | Uma líder nova em até 10 s se a líder cair | Eleição do etcd-raft, `ELECTION_TICKS=30` | Código e compose [F] | CA-09 | POC hoje | — |
| H-02 | Banco lento não derruba a liderança | Laço do Raft sem esperar o apply (ADR-060), commit publicado como sinal | `etcdraft/node.go`, `usecase/replicate.go` [F] | CA-17, CA-19 | POC hoje | — |
| H-03 | Sobrecarga vira recusa, não queda | Admissão por `atraso_apply` (`APPLY_LAG_MAX`) | `usecase/write.go:93-99` [F] | CA-18 | POC hoje | — |
| H-04 | O processo não cai por memória do log | Compactação da RAM, teto `RAFT_RAM_MAX`, `GOMEMLIMIT`, `mem_limit`, fila por par limitada | Canvas O-33 a O-35, O-37 (DC-09) | CA-21, CA-23, CA-27 | Canvas | **Hoje o log cresce sem limite em RAM** (o OOM do problema original). CA-23 e CA-27 mudaram de forma (DC-08, contagem e soak) |
| H-05 | A partida não cresce com o total escrito | `wal.Open` a partir do último snapshot | Canvas O-36 | CA-22 | Canvas | — |
| H-06 | Uma follower atrás do snapshot não entra em crash-loop | `MsgSnap` aceito ou descartado sem erro fatal, e recuperação iniciada | Canvas O-36 (DC-01) | CA-24 | Canvas | **Hoje um snapshot recebido é fatal** [F]. Por isso o incremento A já inclui esse tratamento |
| H-07 | A Azure vota mas não vira líder por eleição | Supressão da candidatura na follower Azure (DH) | Nada no código | Nenhuma CA | Fora (decidido) | Desvio do uso normal do etcd-raft; precisa de teste próprio |
| H-08 | A liderança fica na célula que fecha a maioria mais rápido | Transferência de liderança por latência (DC da rodada 2a) | Removida da POC (commit `273b9cc`) | Nenhuma CA | Fora (decidido) | — |

## 4. Recuperação do dado

| # | Garantia | Mecanismo | Onde | Prova | Estado | Lacuna |
|---|---|---|---|---|---|---|
| C-01 | Uma follower reiniciada alcança as outras sem mudar o dado | Replay do WAL a partir do progresso, apply idempotente | Código atual [F] | CA-08 | POC hoje | Com a compactação, o replay passa a vir do disco pelo `LogReader` (canvas O-34) |
| C-02 | Uma célula atrasada dentro da janela se recupera pelo log, fora do laço do Raft | `Retained` + `CatchUp`, `Recovery`, `CommitPump` em modo remoto | Canvas O-38, O-39 (DC-02, DC-03) | CA-23, CA-24 | Canvas | — |
| C-03 | Uma célula fora da janela se reconstrói sem parar as escritas | `CopyTable` de uma célula sã até F, sobrescrita no lugar pelo `PutItem` condicional (DC-11), progresso F com fsync, reinício, depois `CatchUp` desde F+1 | Canvas O-40, O-41 (DC-04, DC-10, DC-11) | CA-25 | Canvas | Recusa com `cluster_id`/`epoch` diferentes. Delete futuro exige lápide (ver nota 1) |
| C-04 | Uma reconstrução que estoura o disco é abortada sem afetar as escritas | Teto duro solta os pinos; nova tentativa depois de `REBUILD_RETRY` (DN) | Canvas O-35, O-41 | CA-26 | Canvas | — |
| C-05 | Um progresso corrompido não faz a célula aplicar errado | Registro com CRC; corrompido é tratado como ausente | `progress/progress.go` [F] | CA-15 | POC hoje | — |
| C-06 | Uma célula não sobe com o WAL de outro grupo | Metadado `{node_id, cluster_id}` (RT-20); checagem de `PEERS` × WAL (RT-10) | Canvas O-33; RT-10 no código [F] | CA-12 | Parcial | — |
| C-07 | Failover para a Azure com a AWS inteira fora | Promoção por API da célula Azure com o maior índice durável; grupo recriado só com a Azure; a AWS volta como célula nova (DL, seção 4 da solução) | Nada no código | Nenhuma CA | Fora (decidido) | Risco aceito: comando dado com a AWS viva cria duas líderes (DL) |
| C-08 | Entrada de célula nova sem reiniciar as outras | ConfChange pelo log, célula sem voto que copia e alcança e depois vira votante (DD') | Nada no código. A checagem `ErrMembership` continua | Nenhuma CA | Fora (decidido) | O caminho de entrada é o mesmo da reconstrução (C-03), então o canvas adianta metade |
| C-09 | Uma célula cujo volume do Raft foi perdido volta com segurança | Na teoria do Raft, uma célula que volta com o mesmo ID e o disco vazio esquece o voto e o termo e pode votar duas vezes no mesmo termo. A saída é removê-la e adicioná-la de novo (ConfChange) | POC: fail-fast na partida (DC-12, canvas O-42); devolução segura no parking lot ([adr-troca-de-pvc-em-cell-ativa](../../parklot/adr-troca-de-pvc-em-cell-ativa.md)) | `task test-lost-volume` (futura CA-28) | Canvas (fail-fast); feature futura | A célula fica fora até `task clean`; o grupo segue com 2 de 3 |
| C-10 | Recuperação de perda ou corrupção lógica do dado (tabela apagada, bug que grava errado em todas as células) | Backup e restauração a um ponto no tempo | Fora do produto: feitos por outro escopo, inclusive o backup do WAL (P-04) | — | **Fora (decidido)** | A replicação copia o erro para todas as células. Restaurar um backup do WAL numa célula de um grupo ativo equivale a perder o volume (C-09), e o fail-fast da DC-12 não detecta um WAL antigo |

Nota 1 (P-02 resolvida em 18/10, DC-11): a reconstrução sobrescreve no lugar a tabela que existe, com o `PutItem` condicional. A tabela sombra (D8) e o apagar e recriar (AP-18) ficam fora. Limites aceitos: um item apagado na fonte continuaria vivo (hoje não há delete; a lápide com `_dr_v` fica para quando ele entrar) e uma história diferente é recusada. O D8 e o AP-18 serão atualizados no `/spdd-sync`.

## 5. Resumo

| Estado | Linhas |
|---|---|
| POC hoje | G-01 a G-06, R-01, H-01 a H-03, C-01, C-05 (12) |
| Canvas, ainda sem código | R-02, R-03, H-04 a H-06, C-02 a C-04, C-09 (fail-fast) (9) |
| Parcial | C-06 (1) |
| Fora (decidido) | R-04, R-05, H-07, H-08, C-07, C-08, C-10 (7) |
| Não discutido | — (0) |

O que isso significa:
1. **Replicação e RPO=0 por célula (3 AWS):** a lógica já está no código. Com o canvas implementado, ela continua valendo com a compactação ligada e sem o OOM de hoje.
2. **RPO=0 contra a perda de provedor:** nada está implementado. Depende de R-04 (regra K=1 da Azure) e de C-07 (promoção). É uma entrega própria, depois do canvas.
3. **Alta disponibilidade:** hoje há duas falhas conhecidas que derrubam uma célula: o OOM (H-04) e o snapshot recebido fatal (H-06). As duas estão no incremento A.
4. **Recuperação:** o canvas cobre célula atrasada e célula fora da janela. Ficam abertos a perda do volume do Raft (C-09) e a proteção contra erro lógico (C-10).

## 6. Decisões pendentes

| # | Pergunta | Afeta |
|---|---|---|
| P-01 | ~~Aprovar DC-04, DC-05, DC-06, DC-08 e DC-10~~ **Aprovado em 18/10** | R-02, R-03, H-04, C-03 |
| P-02 | ~~Tabela sombra ou apagar e recriar?~~ **Resolvido em 18/10: sobrescrever no lugar (DC-11)** | C-03 |
| P-03 | ~~Célula com o volume do Raft perdido?~~ **Resolvido em 18/10: fail-fast (DC-12); remover e adicionar vira a feature `troca-de-pvc-em-cell-ativa` (parking lot)** | C-09 |
| P-04 | ~~Backup ou PITR no escopo?~~ **Resolvido em 18/10: fora do produto; backup do banco e do WAL é de outro escopo** | C-10 |
| P-05 | ~~Ordem das entregas?~~ **Resolvido em 18/10: ordem 1 — canvas A → canvas B → ConfChange e `troca-de-pvc-em-cell-ativa` (C-08, C-09) → multicloud (R-04, R-05, H-07, C-07)**. O multicloud vem por último porque a volta da AWS depois da promoção usa a ConfChange | Planejamento |

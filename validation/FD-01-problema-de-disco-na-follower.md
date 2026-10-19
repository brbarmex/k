# FD-01 · [disco] Problema de disco na célula follower (comando ou desanexar)

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha |
| Origem | Lista do Bruno |
| Requisitos | FT, RPO = 0, HA (as outras seguem) |
| Suporte na POC hoje | **Parcial.** Falha de fsync é fatal e derruba o processo (`wal.Save` → erro → `laço de fundo parou`), como manda o ADR-034. **Mas a célula volta com o mesmo NODE_ID e o mesmo WAL**, o que o ADR-034 proíbe: depois de um fsync que falhou, não se sabe o que de fato está no disco. |
| Injeção | três variantes, porque "comando ou desanexar" são falhas diferentes (abaixo) |
| Duração | 10 min antes, falha, até a volta + 10 min |

## Análise crítica: "problema de disco" são três falhas com comportamentos opostos

| Variante | Como se provoca | O que o app vê | Esperado |
|---|---|---|---|
| a. **Pausa de I/O** | `aws:ebs:pause-volume-io` no volume do WAL, 30 s e 120 s | o fsync **não volta** (fica preso) | o laço do Raft da follower para; a líder segue com a outra follower; ao fim da pausa, a follower continua sem erro |
| b. **Erro de I/O / desanexar** | `aws ec2 detach-volume --force` com o volume montado; ou Chaos Mesh `IOChaos` com `errno: EIO` no diretório do WAL | o fsync **devolve erro** | processo cai (fatal); pod reinicia; o volume pode não voltar (pod preso em `ContainerCreating`) |
| c. **Sistema de arquivos só leitura** | `mount -o remount,ro` do ponto de montagem no nó (via `aws:ssm:send-command`) | escrita devolve `EROFS` | igual a b, mas o volume continua anexado |

- Na variante **a**, o comportamento depende do `nvme_core.io_timeout` do sistema do nó: no Amazon Linux ele é muito alto e a I/O só espera; em outras imagens, a I/O pode estourar o timeout e virar a variante b. Registrar a imagem e o valor.
- Na variante **a**, a follower parada não é detectada pela sonda: `/livez` responde sempre e `/readyz` continua 200 (ela ainda "conhece a líder"). As **escritas** que chegam a ela continuam funcionando, porque o encaminhamento à líder usa o cliente gRPC, fora do laço preso (inferido do código). As **leituras fortes** não: o commit publicado e o apply local param junto com o laço, a barreira nunca é alcançada e a leitura volta `read_timeout` no prazo. É um problema de HA de leitura que a lista original não pega.
- Na variante **b**, o `detach-volume --force` é uma operação que a própria AWS avisa que pode corromper o sistema de arquivos. É exatamente o que se quer testar, mas pode cair em FD-06 (WAL com cauda corrompida) e a célula não subir mais.
- Nas variantes **b** e **c**, ao voltar com o mesmo volume, a célula reusa a identidade. O cenário precisa de uma **evidência indireta** de perda: comparar o último índice do WAL na partida (campo `ultimo` do log `partida: WAL relido`, M-05) com o `dr_peer_match_index{par=<follower>}` que a líder tinha antes da falha. Se o WAL voltou **menor** que o que a follower já tinha confirmado à líder, ela confirmou algo que não guardou: violação de segurança, mesmo que o oráculo desta execução não acuse perda (porque a outra follower tinha o dado).

## Passos

1. CF-03 com CRG-1.
2. Registrar `dr_peer_match_index` da follower-alvo na líder (série contínua).
3. T0: aplicar a variante.
4. a: aguardar o fim da pausa. b/c: devolver o volume ou remontar; deixar o pod voltar.
5. 10 min de carga; quiescência; oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Sem eleição | termo constante |
| A3 | Sem impacto nas outras entradas | p99 de L-ACK via líder e outra follower ≤ 1,2 × baseline |
| A4 | Variante a: duração do travamento visível **durante** o travamento | M-15 (`dr_wal_fsync_inflight_seconds`); sem M-15: **não comprovado** |
| A5 | Variantes b e c: processo cai na primeira falha de fsync, sem repetir | log `etcdraft: wal.Save`; `kube_pod_container_status_last_terminated_reason` |
| A6 | Variantes b e c: sem regressão do WAL | `ultimo` do log `partida: WAL relido` (M-05) ≥ último `dr_peer_match_index` visto pela líder; se menor: **FALHA de segurança** (ADR-034) |
| A7 | Requisições presas na follower medidas | contagem e max de L-ACK das que entraram por ela |

## Métricas

### Latência (p90, p99, p99.9, max)

| Sinal | Observação |
|---|---|
| L-FSYNC da follower | na variante a, um único ponto do tamanho da pausa, observado só no fim |
| L-LOOP, L-TICK da follower | idem |
| L-ACK por célula de entrada | |
| L-EBS (node-exporter) | confirma que a pausa foi no disco |

### Golden signals

- Tráfego: G-TRF-1 da follower (cai a zero ou fica preso).
- Erros: G-ERR-1, G-ERR-2, G-ERR-4, G-ERR-5 nas requisições da follower.
- Saturação: G-SAT-1 da follower (requisições presas), G-SAT-6 (CloudWatch `VolumeQueueLength`, estado do volume).

### Além dos golden signals

B-RAFT-3 (`dr_peer_lag` da follower), `dr_peer_send_dropped_total` na líder (fila do par cheia), B-K8S (eventos de volume), M-05, M-15.

## Lacunas

M-05 e M-15 são obrigatórias para este cenário ser comprovado por observabilidade. Identidade nova após falha de fsync (ADR-034) não existe na POC.

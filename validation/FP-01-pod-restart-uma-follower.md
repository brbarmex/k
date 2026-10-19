# FP-01 · [pod-restart] Queda de uma célula follower

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha |
| Origem | Lista do Bruno |
| Requisitos | FT (a maioria segue), HA (sem interrupção), RPO = 0 |
| Suporte na POC hoje | **Sim.** Duas células formam maioria; a follower volta, relê só a cauda do WAL desde o último snapshot e retoma o apply a partir do progresso salvo (`ResumeFrom`); o que estiver entre o progresso e o snapshot vem do disco pelo `LogReader`. Teste local equivalente: `task test-follower-restart`. |
| Injeção | três variantes: **a** gracioso (`aws:eks:pod-delete` / `kubectl delete pod`, SIGTERM), **b** abrupto (`kubectl delete pod --grace-period=0 --force` ou `kill -9` no processo), **c** queda do container sem troca de pod (`kill 1` no container: reinício pelo kubelet no mesmo nó, mesmo volume) |
| Duração | 10 min de CF-03 antes, falha, 10 min depois |

## Análise crítica

- "pod-restart" precisa dizer **como**: SIGTERM, SIGKILL e reinício do container têm caminhos diferentes. Em "a", o app fecha o WAL e o progresso com ordem (`OnStop`); em "b", o progresso (gravado sem fsync) pode voltar a um valor antigo, o que só alonga o replay, que é idempotente pela versão. O cenário roda as três.
- O pod reiniciado volta ao **mesmo nó** se ainda houver espaço; senão vai para outro nó da mesma AZ e o volume EBS precisa ser desanexado e anexado de novo. Esse tempo de anexação entra no MTTR da célula e precisa ser registrado (FP-08 trata o caso de nó perdido).
- O tráfego que chega à follower caída é perdido pelo cliente (conexão recusada ou 503 do balanceador). Isso não é RPO, é disponibilidade **daquela entrada**: em produção o roteador deveria tirar a célula da rota. Medir quantas requisições foram afetadas e por quanto tempo.
- Ao voltar, a célula fica pronta (`/readyz`) assim que conhece a líder, **antes** de alcançar o apply. Leituras fortes que caem nela esperam o apply e podem dar `read_timeout`. Com CRG-3, medir isso.

## Passos

1. CF-03 (CRG-1, depois repetir com CRG-2).
2. T0: derrubar uma follower (a, b, c em execuções separadas).
3. Manter a carga por 10 min.
4. Quiescência, oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Sem eleição | `increase(dr_leader_changes_total)` = 0 na líder e na outra follower; termo constante |
| A3 | Escritas pela líder e pela outra follower sem interrupção | RTO-08 = 0 para essas entradas; p99 de L-ACK ≤ 1,2 × baseline |
| A4 | MTTR da célula | RTO-07 registrado e decomposto: fim do processo → pod criado → volume anexado → WAL relido (M-05) → pronta → alcançou |
| A5 | Quórum na líder | `dr_quorum_active` cai para 2 e volta para 3; `dr_quorum_ok` sempre 1 |
| A6 | Replay idempotente | variante b: nenhum erro de apply; `ConditionalCheckFailed` absorvido (M-07) |

## Métricas

### Latência (p90, p99, p99.9, max), antes / durante / depois

| Sinal | Observação |
|---|---|
| L-ACK por célula de entrada | na follower caída: max e contagem de erros, sem percentil (poucas amostras) |
| L-FSYNC, L-LOOP na líder | não devem mudar |
| L-APPLY na célula que volta | o replay dá a curva de alcance |
| L-READ (CRG-3) na célula que volta | |
| L-REPL da célula que volta | tempo até alcançar |

### Golden signals

- Tráfego: G-TRF-1 por célula (zero na caída), G-TRF-2.
- Erros: G-ERR-2 e G-ERR-5 (só nas requisições da célula caída), G-ERR-4 (leituras na que volta).
- Saturação: G-SAT-2 e G-SAT-3 na que volta (backlog), G-SAT-5 (a líder segura as entradas na memória enquanto ela está fora).

### Além dos golden signals

B-RAFT-2, B-RAFT-3 (`dr_peer_lag` da caída sobe e depois zera), B-RAFT-4 (`app` com rejeição e reenvio), B-DRENO, B-K8S (reinício, motivo, tempo de attach), M-05, M-06.

## Prova de RPO = 0

ORC-04 com a célula de entrada; INCERTAS só nas requisições que estavam encaminhadas pela follower no instante da queda.

## Lacunas

M-05, M-06, M-07, M-11.

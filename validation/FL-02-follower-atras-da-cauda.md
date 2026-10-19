# FL-02 · Follower atrás da cauda da líder (o caminho do `MsgSnap`)

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do log (complemento) |
| Origem | Complemento: é a CA-24 e a DC-08 da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md) |
| Requisitos | RPO = 0 (SG-19: nenhuma célula aceita um marcador à frente do seu banco), FT, HA (a célula volta) |
| Suporte na POC hoje | **Parcial.** Incremento A: a líder envia o marcador e a follower o descarta com o aplicado atrás (SG-19), **sem cair**; ela fica parada em `raft.atrasCauda` no `/status`. A recuperação pelo canal `CatchUp` é do Incremento B (O-38, O-39). |
| Injeção | três variantes que levam a follower para trás da RAM da líder (abaixo) |
| Duração | 10 min antes, falha, até o desfecho + 10 min |

## Análise crítica

- A líder só segura a RAM para uma follower **dentro da janela** e até `RAFT_RAM_MAX`. Fora disso, o `MemoryStorage` da líder começa depois do que a follower tem, e o Raft manda `MsgSnap`.
- Três caminhos chegam a esse estado, com ritmos diferentes:

| Variante | Como se provoca | Por que a follower fica atrás |
|---|---|---|
| a. Líder reiniciada | follower fora (FP-04, `replicas=0`) por alguns snapshots; reiniciar a líder; devolver a follower | a líder volta só com a cauda desde o último snapshot (DC-08) |
| b. Teto da RAM | follower fora com CRG-2 até `raft.memoria.bytes` da líder chegar a `RAFT_RAM_MAX` | no teto, a líder compacta até o snapshot ignorando o `Match` (SG-23) |
| c. Troca de líder | follower fora; derrubar a líder (FP-03); a nova líder, que como follower já compactava a própria RAM, assume | a nova líder nunca segurou RAM para ninguém |

- No Incremento A, as três variantes terminam com a follower **parada, sem perder nada e sem derrubar o processo**: é o critério de hoje. Com 2 células saudáveis, o sistema segue, mas sem margem para outra falha (tempo em risco sem fim, até intervenção).
- No Incremento B, a mesma execução deve terminar com a follower em `recuperando`, alcançando pelo `CatchUp` e voltando a `normal`.

## Passos

1. CF-03 com CRG-1 (variantes a e c) ou CRG-2 (variante b).
2. Aplicar a variante.
3. Devolver a follower e observar por 10 min.
4. Quiescência das células saudáveis; oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 (sobre as 2 células saudáveis; no Incremento A a 3ª fica atrás) |
| A2 | Nenhum marcador chega ao Raft com o aplicado atrás | `dr_raft_snapshot_deferred_total` crescendo na follower; `raft.snapshot` dela sem pular; log `marcador adiado` |
| A3 | A follower não cai | `kube_pod_container_status_restarts_total` sem aumento; nenhum log `fatal` |
| A4 | Estado visível | `/status` `raft.atrasCauda == true` na follower |
| A5 | Incremento A: sem efeito nas outras | escritas pela líder e pela outra follower com p99 ≤ 1,2 × baseline; sem eleição (variantes a e b) |
| A6 | Incremento B: recuperação | `celula.estado` passa por `recuperando` e volta a `normal`; `aplicado` igual ao da líder; ORC-04 sobre as 3 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK; no Incremento B, a vazão do `CatchUp` (`dr_catchup_entries_total`).
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-5 da líder (a RAM não pode passar de `RAFT_RAM_MAX`).
- Além: B-RAFT-3 (`dr_peer_lag` da follower parado), B-RAFT-4 (`snap` enviado pela líder, com taxa limitada no log `marcador enviado`), `raft.memoria` da líder, `wal.janela`.

## Lacunas

No Incremento A, falta a recuperação (o cenário prova a lacuna de forma segura). O tempo de alcance só é mensurável no Incremento B.

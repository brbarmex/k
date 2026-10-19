# FL-05 · Queda do processo no meio de um snapshot

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do log (complemento) |
| Origem | Complemento: é a ordem do RT-11 e a DC-06 da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md) |
| Requisitos | RPO = 0 (nenhum passo do snapshot perde entrada nem faz o confirmado recuar), HA (a célula volta sozinha) |
| Suporte na POC hoje | **Sim** (Incremento A). A ordem é fsync do progresso, arquivo `snapshot` (tmp, fsync, rename), `wal.SaveSnapshot`, `CreateSnapshot`, compactação da RAM e liberação do disco. Uma queda entre os passos só deixa um marcador mais antigo como ponto de partida. Coberto por teste de unidade (marcador à frente do WAL). |
| Injeção | `kill -9` no processo em rajadas durante CRG-2, com `SNAPSHOT_EVERY_ENTRIES` baixo (ex.: 200) para haver snapshot a cada poucos segundos; repetir até cair perto de um log `snapshot gravado` (o instante do log e o do kill no ledger do injetor) |
| Duração | 30 repetições por célula (líder e follower) |

## Análise crítica

- A janela de cada passo é curta: o cenário depende de repetição. O critério é estatístico: em N quedas, nenhuma célula deixa de subir e o oráculo não acusa nada.
- O caso mais delicado é a queda entre o arquivo do marcador e o `wal.SaveSnapshot`: o marcador fica à frente do WAL. A partida usa o snapshot do WAL com o confirmado do marcador.
- Uma queda depois do `ReleaseLockTo` e no meio do `os.Remove` deixa segmentos que deviam ter saído: o próximo ciclo os remove. Não pode haver segmento faltando **antes** do último snapshot do WAL.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | A célula sempre volta | 0 `CrashLoopBackOff`; log `partida: WAL relido` em cada partida |
| A3 | Confirmado não recua | `dr_gate_index` da célula logo após a partida ≥ o último `confirmado` logado em `snapshot gravado` antes da queda |
| A4 | Partida curta | `dr_startup_duration_seconds` sem crescer com as quedas |

## Métricas

- Golden signals: G-ERR-1..5, G-TRF-2.
- Além: B-K8S (reinícios), `dr_startup_duration_seconds`, `raft.snapshot` antes e depois, logs `snapshot gravado` e `partida: WAL relido`.

## Lacunas

M-05 (sem série para o último índice; o log `partida: WAL relido` traz `primeiro` e `ultimo`).

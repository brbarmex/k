# CE-02 · Líder congelada que volta achando que ainda é líder

| Campo | Valor |
|---|---|
| Categoria | Caminho eventual (complemento) |
| Origem | Complemento: é a forma "abrupta" de troca que não derruba o processo |
| Requisitos | RPO = 0 (nenhum ACK falso), FT |
| Suporte na POC hoje | **Sim.** O Raft com `CheckQuorum` e `PreVote` recusa a líder antiga quando ela volta com termo menor. O que precisa ser provado é que ela não confirma nada sozinha. |
| Injeção | `kill -STOP` no processo da líder por 5 s, 15 s e 60 s, depois `kill -CONT` (via `aws:ssm:send-command` no nó ou `kubectl debug` com processo compartilhado). Alternativa: `aws:eks:pod-cpu-stress` extremo. |
| Duração | 10 min antes, pausa, 10 min depois |

## Objetivo

Provar que uma líder pausada (GC longo, nó sem CPU, VM congelada) e retomada depois que as outras elegeram outra líder não confirma escrita, não aplica nada fora do gate e passa a follower.

## Análise crítica

- Durante a pausa, a célula não responde nem a sondas: o Kubernetes pode ou não reiniciá-la, dependendo de `livenessProbe` (`failureThreshold × periodSeconds`). Registrar qual foi o caso; os dois são válidos, mas medem coisas diferentes.
- Na volta, as requisições que estavam abertas na líder antiga terminam com PENDING ou com ACK. Um ACK é **correto** se a entrada foi de fato confirmada pela nova líder (a líder antiga mandou o MsgApp antes do fsync e uma follower pode ter persistido): a POC casa o ACK pelo `ProposalID` da entrada confirmada. O oráculo é quem decide se o ACK era verdadeiro.
- O Prometheus não consegue coletar a célula pausada: o buraco na série é a evidência da pausa, não uma falha de coleta.

## Passos

1. CF-03 com CRG-1.
2. T0: SIGSTOP na líder.
3. Após 5 s (acima do timeout máximo de eleição, 2 s) → SIGCONT. Repetir com 15 s e 60 s. Uma rodada extra com 0,5 s (abaixo do timeout) deve terminar **sem** eleição.
4. Quiescência, oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0, sem ACK falso | ORC-04: toda CONFIRMADA vinda da líder antiga existe nas 3 tabelas |
| A2 | Nunca duas líderes no mesmo termo | B-INV-2 |
| A3 | A antiga vira follower em até 1 timeout de eleição após o SIGCONT | `dr_is_leader` e `dr_raft_term` da célula |
| A4 | Delta-T medido | RTO-04 com pausa de 15 s e 60 s ≈ timeout de eleição, não a duração da pausa |
| A5 | Gate não recua na antiga | B-INV-3 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por célula de entrada (as requisições presas na antiga dão o max), L-TICK da antiga (um único intervalo do tamanho da pausa), L-LOOP.
- Golden signals: G-TRF-2, G-ERR-1, G-ERR-2, G-ERR-5, G-SAT-1.
- Além: B-RAFT-1, B-RAFT-4 (`prevote` recusado), B-RAFT-5, B-INV-1..3, B-K8S (reinício por liveness).

## Lacunas

M-14, M-15 (sem ele, a pausa só aparece depois que acaba).

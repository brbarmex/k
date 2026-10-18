# FP-02 · [pod-restart] Queda de todas as células follower

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha |
| Origem | Lista do Bruno |
| Requisitos | RPO = 0 (principal), FT no limite; **HA não é esperada** |
| Suporte na POC hoje | **Sim, como perda de quórum.** Com 1 de 3, nada é confirmado; a líder deixa de ser líder por `CheckQuorum` após o timeout de eleição; as escritas recebem 503 sem líder (depois de `WAIT_LEADER`) ou PENDING. Teste local equivalente: `task test-no-quorum`. |
| Injeção | as duas followers ao mesmo tempo, variantes a/b/c de FP-01 |
| Duração | 10 min antes, reinício, 10 min depois |

## Análise crítica

- Com 3 votantes, perder 2 é perder a maioria: o sistema é CP e **deve** parar de confirmar. O cenário não valida alta disponibilidade, valida que **nada é confirmado sem maioria** e que a volta é limpa. O critério de "passar" precisa refletir isso, senão o cenário parece um fracasso de HA.
- "Ao mesmo tempo" precisa de definição: o FIS dispara as duas ações em paralelo, mas os pods param em instantes diferentes. Registrar a diferença.
- Ponto oculto: escritas propostas pela líder **antes** de perceber a perda de quórum ficam no WAL dela sem confirmação e o cliente recebe PENDING. Se essa mesma célula for reeleita na volta (ela tem o log mais longo), essas entradas **são confirmadas depois**, minutos após o cliente ter desistido. São INCERTAS que aparecem. Os SDKs que repetiram geram uma segunda entrada. O oráculo precisa contar esse caso.
- Na líder sozinha, `/readyz` passa a falhar ("sem líder conhecida"). No Kubernetes, isso tira a célula do Service: o cliente passa a receber erro do balanceador ou conexão recusada, **não** o 503 do app com `x-dr-code`. A classe de erro vista pelo cliente muda, e o cenário deve registrar qual chegou.
- Requisições esperando líder são limitadas por `MaxWaitingLeader`; acima do teto viram 503 na hora. Medir se o teto é atingido.

## Passos

1. CF-03 com CRG-1.
2. T0: derrubar as duas followers.
3. Medir até o retorno das duas.
4. 10 min de carga depois; quiescência; oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Nenhum 200 entre "perda da maioria" e "volta da maioria" | ledger: zero CONFIRMADA com instante de resposta nessa janela, exceto as já confirmadas antes |
| A2 | RPO = 0 | ORC-04; contar INCERTAS presentes (confirmadas depois) e ausentes |
| A3 | Líder sozinha deixa de ser líder | `dr_leader_id == 0` na sobrevivente em ≤ 2 × timeout de eleição após T0 |
| A4 | Volta limpa | uma única líder com `dr_quorum_ok == 1`; RTO-04 a partir da volta da segunda célula |
| A5 | Sem acúmulo de requisições presas | `dr_http_inflight` volta a valores de baseline; sem OOM na sobrevivente |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK antes e depois (durante: max e contagem por classe de erro), L-HTTP das respostas de erro (devem ser rápidas: ~`WAIT_LEADER`), L-TICK.
- Golden signals: G-TRF-1, G-TRF-2 (zero na janela), G-ERR-1 (`no_leader`, `pending`), G-ERR-2 (503, 500), G-ERR-5 (incluindo erros do balanceador), G-SAT-1, G-SAT-5.
- Além: B-RAFT-1, B-RAFT-2, B-RAFT-4, `kube_pod_status_ready` das 3, RTO-02..08.

## Lacunas

M-09 (latência das respostas de erro), M-14.

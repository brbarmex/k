# FP-04 · [pod-outage-10min] Queda de uma célula follower por 10 minutos

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha |
| Origem | Lista do Bruno |
| Requisitos | FT, HA, RPO = 0; e o tempo de **reconvergência** |
| Suporte na POC hoje | **Sim com CRG-1; Parcial com CRG-2 até o Incremento B.** Desde o Incremento A (`eec02c9`), a líder segura a RAM para a follower parada (dentro da janela) só até `RAFT_RAM_MAX` (256 MiB). Com CRG-1 (cerca de 180 MiB em 10 min a 300 TPS e 1 KiB) cabe e a follower alcança por `MsgApp`. Com CRG-2 (cerca de 1,8 GiB) passa do teto: a líder compacta, a follower volta atrás da cauda, descarta o marcador e fica parada em `raft.atrasCauda` (FL-02), sem cair, até a recuperação do Incremento B. Teste local parecido: `task test-follower-outage` (10 s, não 10 min). |
| Injeção | `kubectl scale statefulset datareplication --replicas=0` no cluster da follower (a célula some e o volume fica), 10 min, depois `--replicas=1` |
| Duração | 10 min antes, 10 min fora, até a reconvergência + 10 min |

## Análise crítica

- O ponto do cenário não é a queda (igual a FP-01), é a **volta depois de 10 min**. Conta inferida para CRG-2 (3000 TPS, ~1 KiB): são ~1,8 milhão de entradas e ~1,8 GiB a reenviar e a aplicar. O reenvio é rápido (MsgApp de 1 MiB, até 256 em voo). O apply é o gargalo: com `APPLY_WORKERS=16` e ~5 a 10 ms por PutItem no DynamoDB, a vazão do apply fica entre ~1600 e ~3200 itens/s, **na mesma ordem da entrada**. Se for menor que a entrada, a follower **nunca** alcança. B-DRENO decide.
- Enquanto ela alcança, o sistema roda com 2 células confirmando e uma terceira sem dado útil: uma segunda falha nesse intervalo (FP-11) derruba a disponibilidade. O "tempo em risco" é métrica de primeira classe.
- A admissão só olha a líder: a follower atrasada nunca recusa nada e não há sinal ao cliente.
- A líder carrega até `RAFT_RAM_MAX` de entradas a mais na RAM; acima disso, o resto fica só no disco (WAL retido dentro da janela, `RETAIN_MAX_BYTES`).

## Passos

1. CF-03 com CRG-1 e, em outra execução, CRG-2.
2. T0: tirar a follower.
3. T0 + 10 min: devolver.
4. Esperar `aplicado == commit da líder`; mais 10 min de carga; quiescência; oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Sem eleição e sem impacto nas outras entradas | como FP-01 A2 e A3 |
| A3 | Reconvergência termina | `dr_applied_index` da follower = `dr_commit_index` da líder em tempo finito; B-DRENO > 0 durante o alcance. Com CRG-2, até o Incremento B: **esperado FALHAR** com a follower em `raft.atrasCauda` (registrar como lacuna, não como defeito) |
| A4 | Tempos medidos | alcance do log (`dr_peer_lag` → 0) e alcance do apply (`dr_apply_lag` → 0) separados; "tempo em risco" = T0 → apply alcançado |
| A5 | Líder não sofre | L-LOOP e L-TICK da líder no alcance ≤ 1,2 × baseline (o reenvio de MsgApp não pode atrasar o laço) |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK; L-LOOP e L-FSYNC da líder durante o alcance; L-APPLY da follower; L-REPL.
- Golden signals: G-TRF-2; G-ERR-1..5; G-SAT-2 e G-SAT-3 da follower; G-SAT-5 da líder; G-SAT-7 (WCU da tabela da follower no alcance).
- Além: B-RAFT-3 (`dr_peer_lag` da follower), B-RAFT-4 (`app` volume), B-REDE (bytes de alcance), B-DRENO, M-04, M-11.

## Lacunas

M-04, M-11.

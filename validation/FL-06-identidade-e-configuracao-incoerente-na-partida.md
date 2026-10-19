# FL-06 · Identidade do WAL e configuração incoerente na partida

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha de operação (complemento) |
| Origem | Complemento: é a RT-20 e as regras de coerência da S.6 da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md) |
| Requisitos | RPO = 0 (uma célula nunca sobe com o WAL de outra história), HA (o erro é claro e não derruba as outras) |
| Suporte na POC hoje | **Sim** (Incremento A). |
| Injeção | variantes de operação, cada uma numa follower, em ambiente isolado (abaixo) |
| Duração | curta, por variante |

## Variantes

| Variante | Como se provoca | Esperado |
|---|---|---|
| a. `CLUSTER_ID` diferente | trocar o `CLUSTER_ID` de uma célula e reiniciar | não sobe: `identidade do WAL não confere` (ou marcador de outro grupo); as outras seguem |
| b. Volume de outra célula | montar o PVC da célula 2 na célula 3 (mesmo `CLUSTER_ID`, `NODE_ID` diferente) | não sobe: `ErrIdentity` com o nó gravado e o configurado |
| c. WAL do formato antigo | subir a versão nova sobre um volume criado antes do commit `2ba474d` | não sobe: `WAL de formato antigo; rode task clean` |
| d. Marcador corrompido | inverter um byte do arquivo `snapshot` com o pod parado | não sobe: `marcador do snapshot inválido` |
| e. Configuração incoerente | `RAFT_CATCHUP_ENTRIES ≥ SNAPSHOT_EVERY_ENTRIES`; `RAFT_RAM_MAX ≤ SNAPSHOT_EVERY_BYTES + 32MiB`; `RETAIN_MAX_BYTES ≥ volume × RETAIN_DISK_HARD` | não sobe; a mensagem cita a variável (todas as violações reunidas numa só partida) |
| f. `MsgSnap` de outro grupo | (só com rede compartilhada por engano) uma célula de outro `CLUSTER_ID` alcança a porta gRPC | o marcador é descartado com `marcador de outro grupo descartado`; nada chega ao Raft |

## Análise crítica

- Todas as variantes são falhas de operação que, sem a checagem, trariam dado de outra história para o grupo. O comportamento certo é recusar a partida com uma mensagem que diga o que fazer, e não tentar se corrigir.
- Na variante e, o erro aparece só na partida: uma mudança de configuração em rolagem (CE-07) para na primeira célula, o que é o desejado.
- Variante c mostra que **não há rolagem** do commit `c9f42af` para o atual: a troca exige `task clean` (na AWS, PVCs novos). Ver CE-07.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Recusa com mensagem clara | log da partida com o motivo; `CrashLoopBackOff` só na célula alterada |
| A2 | As outras seguem | `dr_quorum_ok == 1` na líder; escritas com ACK |
| A3 | RPO = 0 | ORC-04 após desfazer a alteração |

## Métricas

- Golden signals: G-TRF-2, G-ERR-1..5.
- Além: B-K8S (motivo do término), logs da partida.

## Lacunas

M-16 (identidade do WAL como rótulo de métrica).

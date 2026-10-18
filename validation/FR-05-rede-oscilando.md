# FR-05 · Rede oscilando entre as células

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha de rede (complemento) |
| Origem | Complemento (R07 do pacote: link oscilando) |
| Requisitos | HA (estabilidade da liderança), RPO = 0 |
| Suporte na POC hoje | **Sim.** |
| Injeção | FR-01 em ciclos: 3 s isolada / 10 s normal, por 5 min; depois ciclos de 1 s / 2 s |
| Duração | 10 min antes, 5 min de oscilação por padrão, 10 min depois |

## Análise crítica

- Cada ciclo pode custar uma eleição. O que importa é o número de eleições por minuto e o tempo total sem líder, não o comportamento de um ciclo isolado.
- Oscilação é o pior caso para o cliente: SDKs repetem e se acumulam.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Eleições por minuto e tempo sem líder | `increase(dr_leader_changes_total[1m])`; soma dos intervalos com `dr_leader_id == 0` |
| A3 | Disponibilidade na janela | RTO-08 somado; % de CONFIRMADAS no ledger |
| A4 | Estabilização após o fim | RTO-06 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK na janela e depois.
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-1.
- Além: B-RAFT-1, B-RAFT-4, B-INV-2.

## Lacunas

M-14.

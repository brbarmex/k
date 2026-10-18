# CF-06 · Soak de longa duração (memória e disco sem compactação do log)

| Campo | Valor |
|---|---|
| Categoria | Caminho feliz no tempo (complemento) |
| Origem | Complemento |
| Requisitos | HA (o processo não pode cair sozinho), RPO = 0 |
| Suporte na POC hoje | **Não, por desenho.** A POC nunca compacta nem tira snapshot do log do Raft: não há chamada a `Compact` nem `CreateSnapshot`, e um `MsgSnap` é recusado (`etcdraft/node.go`, RT-11). O `MemoryStorage` guarda todo o histórico em RAM e o WAL cresce sem limite. |
| Injeção | nenhuma; só tempo |
| Duração | 4 h com CRG-1 (ou até o primeiro OOMKill / disco cheio) |

## Objetivo

Medir a taxa de crescimento de memória e disco e **projetar quando a POC cai**. Não é para passar: é para provar com números a lacuna que o ADR-063 (snapshot e compactação) resolve no produto.

## Análise crítica

- Conta de ordem de grandeza (inferida): a 3000 TPS com ~1 KiB por entrada, são ~10 GiB por hora em RAM em **cada** célula, mais o WAL no disco. A líder e as followers caem por OOMKill em poucas horas, e todas ao mesmo tempo, porque crescem no mesmo ritmo. Uma queda simultânea das 3 é perda total de disponibilidade.
- O tempo de partida também cresce: a partida relê o WAL inteiro (`w.ReadAll`) para o `MemoryStorage`. Depois de horas de carga, um simples reinício (FP-01) passa a demorar minutos. Isso precisa ser medido aqui (M-05) para os cenários de reinício saberem a duração real.

## Passos

1. CRG-1 contínuo (perfil CF-03).
2. A cada 30 min: reiniciar uma follower (FP-01) e medir o tempo de partida.
3. Parar no primeiro OOMKill, disco acima de 90% ou após 4 h.

## Critérios (de medição)

| # | Medida | Fonte |
|---|---|---|
| A1 | Inclinação de RSS (MiB/h) por célula | `deriv(process_resident_memory_bytes[30m])` |
| A2 | Inclinação do disco (GiB/h) | `deriv(kubelet_volume_stats_used_bytes[30m])` |
| A3 | Projeção de OOM e de disco cheio | limite do container e capacidade do PVC ÷ inclinação |
| A4 | Tempo de partida × tamanho do WAL | M-05, `kube_pod_status_ready` |
| A5 | Latência estável ao longo do tempo | L-ACK p99 por hora; GC: `go_gc_duration_seconds` |
| A6 | RPO = 0 até a queda | ORC-04 |

## Métricas

- Latência (p90, p99, p99.9, max) **por hora**: L-ACK, L-FSYNC, L-LOOP, L-TICK, L-APPLY.
- Golden signals: G-TRF-2, G-ERR-1..5, G-SAT-5 (memória), G-SAT-6 (disco).
- Além: M-04 (entradas e bytes no MemoryStorage), B-GO, B-K8S (OOMKilled), M-05.

## Lacunas

M-04, M-05.

## Decisão que este cenário alimenta

Se a POC vai ou não implementar compactação antes da validação final (ADR-063). Sem ela, todos os cenários de 10 min com CRG-2 já carregam um crescimento de memória que precisa ser descontado.

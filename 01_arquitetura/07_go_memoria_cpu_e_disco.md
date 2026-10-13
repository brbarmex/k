# Go, memória, CPU e disco

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 8. Leve e rápido: Go, memória e CPU

### 8.1 Memória
- `GOMEMLIMIT` em ~80% do *limit* do contêiner, `GOGC` ajustado, nunca confiar só no padrão. Medir RSS real, não só o heap.
- **Tudo limitado em bytes**, não em contagem: filas de entrada, buffers de stream, entradas em voo (`MaxInflightMsgs`, `MaxSizePerMsg`, `MaxUncommittedEntriesSize` do Raft), janela de dedupe.
- Zero alocação no caminho quente: `sync.Pool`/buffers reutilizáveis para I/O e codificação; evitar `interface{}` e `map[string]any` no JSON do DynamoDB (decodificar para uma estrutura própria).
- Janela de dedupe (por requisição, ADR-035): com 5 min (D11/D13) × 3.000 TPS ≈ 900 mil entradas × ~64 B ≈ 58 MiB, cerca de um quarto da meta de 256 MiB. Precisa estar no orçamento e é um risco aberto (R22 em 06_pendencias_e_riscos).
- **Pebble (ADR-055):** memtables (proposta 2 × 32 MiB) e cache de blocos (proposta 64 a 128 MiB) ficam **fora do heap do Go** em parte e não são controlados pelo `GOMEMLIMIT`; somam ~130 a 190 MiB. Com o `raft.MemoryStorage` do etcd, as entradas não compactadas do Raft também ficam em memória, limitadas pela folga `RAFT_RETAIN_MAX`.
- Meta (a validar no spike): ≤ 256 MiB RSS com dezenas de shards. **São metas, não medições.** Com o Pebble, a meta de 256 MiB **não fecha** junto com a janela de dedupe (~58 MiB): é preciso revê-la no spike S2 (proposta: 512 MiB).

### 8.2 CPU e agendamento
- O Go respeita o limite de CPU do cgroup para `GOMAXPROCS` nas versões recentes (verificar no 1.27).
- **Throttling de CPU mata o Raft**: heartbeat atrasado vira eleição. Para o `DataReplication`, usar QoS *Guaranteed* (requests = limits) ou sem limit de CPU, e timeouts de eleição folgados em relação a pausas de GC e throttling.
- Um loop por conjunto de shards (e não uma goroutine por shard) para reduzir troca de contexto.
- **Pausas de GC e throttling contam para o lease** do modo degradado ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)): a líder confere a validade a cada ACK, e a pausa máxima de processo medida entra na margem M da promoção. Medir no spike S2 e usar relógio monotônico que conte o tempo de suspensão quando existir.

### 8.3 Disco e I/O
- Volume com latência de fsync previsível. EBS gp3/io2 (EKS) e Azure Premium SSD v2 (AKS). Medir fsync P99 antes de aceitar.
- NVMe local foi descartado pelo Bruno (12/10): o WAL fica em volume de bloco dedicado e provisionado.
- Não usar `O_DIRECT` na v1 (complexidade). Usar `fdatasync`.
- **Dois volumes por nó (ADR-055):** um pequeno e rápido para o log do Raft (fsync no caminho do ACK) e um maior para o Pebble. O volume do Pebble guarda o **progresso do applier por item** `(índice, ordem)` ([ADR-048](../03_adr/ADR-048-skip-por-item.md)) e, durante um rebuild, o log retido desde o índice F ([ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)). O dimensionamento do volume considera esse caso, além do modo degradado longo (teto de 70%, ADR-041).

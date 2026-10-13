# Go, memória, CPU e disco

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 8. Leve e rápido: Go, memória e CPU

### 8.1 Memória
- `GOMEMLIMIT` em ~80% do *limit* do contêiner, `GOGC` ajustado, nunca confiar só no padrão. Medir RSS real, não só o heap.
- **Tudo limitado em bytes**, não em contagem: filas de entrada, buffers de stream, entradas em voo (`MaxInflightMsgs`, `MaxSizePerMsg`, `MaxUncommittedEntriesSize` do Raft), janela de dedupe.
- Zero alocação no caminho quente: `sync.Pool`/buffers reutilizáveis para I/O e codificação; evitar `interface{}` e `map[string]any` no JSON do DynamoDB (decodificar para uma estrutura própria).
- Janela de dedupe: com 5 min (D11/D13) × 3.000 TPS ≈ 900 mil entradas × ~64 B ≈ 58 MiB, cerca de um quarto da meta de 256 MiB. Precisa estar no orçamento e é um risco aberto (R22 em 06_pendencias_e_riscos).
- Meta (a validar no spike): ≤ 256 MiB RSS com dezenas de shards. **São metas, não medições.**

### 8.2 CPU e agendamento
- O Go respeita o limite de CPU do cgroup para `GOMAXPROCS` nas versões recentes (verificar no 1.27).
- **Throttling de CPU mata o Raft**: heartbeat atrasado vira eleição. Para o `DataReplication`, usar QoS *Guaranteed* (requests = limits) ou sem limit de CPU, e timeouts de eleição folgados em relação a pausas de GC e throttling.
- Um loop por conjunto de shards (e não uma goroutine por shard) para reduzir troca de contexto.

### 8.3 Disco e I/O
- Volume com latência de fsync previsível. EBS gp3/io2 (EKS) e Azure Premium SSD v2 (AKS). Medir fsync P99 antes de aceitar.
- NVMe local foi descartado pelo Bruno (12/10): o WAL fica em volume de bloco dedicado e provisionado.
- Não usar `O_DIRECT` na v1 (complexidade). Usar `fdatasync`.

# C21 — Falha de fsync, corrupção, disco lento e disco perdido

**Status:** ✅ Atualizado (modelo v1) · **ADRs:** [ADR-019](../../03_adr/ADR-019-wal-proprio-volume-dedicado.md), [ADR-034](../../03_adr/ADR-034-fsync-fatal-e-identidade-nova.md), [ADR-047](../../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md) · **Relacionados:** [C33](C33-reconstrucao-em-tabela-sombra.md), [04_wal_e_fsync](../../01_arquitetura/04_wal_e_fsync.md)

## Cenário

O disco é o ponto mais sensível: o fsync decide a latência e a segurança do ACK. A política é de falhar rápido e reconstruir, nunca de seguir com dado duvidoso. Há duas reconstruções distintas: a do **nó** (identidade nova, log do Raft e de replicação refeitos por catch-up) e a do **banco da célula**, que segue o protocolo da tabela sombra ([C33](C33-reconstrucao-em-tabela-sombra.md)).

## Diagrama

```mermaid
flowchart TD
    A["Evento de disco"] --> B{"Qual?"}
    B -->|"fsync falhou"| C["Processo encerra, sem retry"]
    B -->|"cauda rasgada na partida"| D["Trunca a cauda e segue"]
    B -->|"corrupção no meio do log"| E["Recusa subir e exige reconstrução"]
    B -->|"fsync lento acima do limiar por N s"| F["Watchdog: líder transfere a liderança"]
    B -->|"disco perdido"| G["Volta com identidade nova como learner"]
    C --> H["Reinicia e reentra pelo log"]
    E --> I["drctl rebuild manual assistido"]
    G --> I
    I --> J["Banco da célula refeito em tabela sombra: F, cópia difusa, replay, cutover e checksum, ver C33"]
```

## Etapas

1. **fsync falhou:** o processo encerra (retry de fsync pode confirmar dado que não está no disco).
2. **Cauda rasgada:** é um registro parcial no fim do segmento; é truncado na partida.
3. **Corrupção no meio:** o nó recusa subir e a réplica é reconstruída.
4. **Disco lento:** um watchdog mede o fsync; acima do limiar por N segundos, a líder transfere a liderança.
5. **Disco perdido:** volta com **identidade nova**, como learner. Nunca reusar o ID antigo com log vazio: o nó pode já ter votado e confirmado entradas.
6. **Banco da célula suspeito ou perdido:** `drctl rebuild` refaz a tabela em **tabela sombra** (registra o índice F, copia sem snapshot, reaplica o log desde F, faz o cutover do nome lógico e confere por checksum por faixa de chaves) antes de religar as leituras. Detalhes, custos e cenários ocultos em [C33](C33-reconstrucao-em-tabela-sombra.md).

## Pontos em aberto

- Reconstrução automática (H-DR1): [R23](../a_rever/R23-hipoteses-gerais.md). Hoje é manual assistido, e o protocolo da tabela sombra é humano em todas as etapas de decisão (pedido, cutover, descarte da tabela antiga).
- P99 de fsync por tipo de volume: spike S1.

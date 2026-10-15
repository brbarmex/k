# D07 — Prazos em cascata dentro dos 30 s do SDK

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice de diagramas](INDEX.md)

O prazo nasce **uma vez**, na borda, e desce como `context`. Cada camada tem prazo menor que a de fora; a partida recusa subir com prazos fora de ordem. O **lease D** do modo degradado está **fora** desta cascata: é da ordem de minutos ou mais (valor a definir), não de segundos. O que entra na cascata é o efeito, a resposta imediata `503 LEASE_EXPIRED` depois de D. O buffer de entradas não persistidas cheio também responde na hora, com `ThrottlingException`, sem consumir o prazo ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)), e o mesmo vale quando o apply local da líder está mais de `APPLY_LAG_MAX` entradas atrás do gate ([ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md)). O resumo de todas as respostas está no [Estado atual](../ESTADO_ATUAL.md), seção 3.9.

## Cascata de prazos

```mermaid
flowchart TD
    SDK["SDK do cliente: 30 s por tentativa, até 3 tentativas"] --> API["REQUEST_DEADLINE 7,5 s"]
    API --> FWD["Forward para a líder: até 3 s"]
    API --> DEG["DEGRADE_AFTER 2 s, só com accept"]
    API --> APPLY["Cliente do banco: 1 s por tentativa, até 2<br/>(no caminho da requisição; o applier repete sem limite, a confirmar)"]
    API --> MARGEM["Margem de resposta: 500 ms ou mais"]
    API -->|"buffer não persistido acima de 32 MiB no shard"| THR["400 ThrottlingException imediato: nada entrou no log, o SDK repete com backoff"]
    API -->|"apply local mais de APPLY_LAG_MAX atrás do gate"| THR
    FWD -->|"sem líder conhecida depois de WAIT_LEADER 1 s"| NL["503 NO_LEADER: nada foi proposto"]
    API -->|"leitura: barreira não alcançada no prazo"| RT["503 READ_TIMEOUT: nunca dado velho"]
    API -->|"estourou depois do commit"| PEND["503 PENDING: não perdeu, o retry é absorvido pelo dedupe"]
    PEND --> DD["Dedupe de 5 min por requisição e versão condicional"]
    DEG -->|"DEGRADE commitado"| LEASE["Lease D, valor a definir: fora da cascata de 30 s"]
    LEASE -->|"fim de D, conferido a cada ACK"| LE["503 LEASE_EXPIRED imediato, dentro do prazo"]
    LEASE -.->|"Azure só promove depois de D mais M desde o último contato"| PROM["ForcePromote recusado antes disso"]
```

## Onde é detalhado

- Tabela completa e consequências: [08_resiliencia_autocura_e_timeouts](../01_arquitetura/08_resiliencia_autocura_e_timeouts.md) (9.4) e [X05](../02_cenarios/complexos/X05-timeouts-e-idempotencia.md).
- Lease, D e M: [03_consenso_raft_quorum_e_gate](../01_arquitetura/03_consenso_raft_quorum_e_gate.md) (5.3.2) e [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md).
- Decisões: [ADR-028](../03_adr/ADR-028-timeouts-em-cascata.md), [ADR-039](../03_adr/ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md), [ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md), [ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md) e [ADR-060](../03_adr/ADR-060-laco-do-raft-nunca-espera-o-apply-e-admissao-por-atraso.md).
- O limite do cliente do banco no applier é o ponto 5 do [inventário dos ADRs](../03_adr/INVENTARIO.md#pontos-a-decidir-perguntas-para-o-bruno).

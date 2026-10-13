# C18 — Partida, recuperação e parada limpa do nó

**Status:** ✅ Atualizado (modelo v1) · **ADRs:** [ADR-018](../../03_adr/ADR-018-dois-logs-raft-e-replicacao.md), [ADR-019](../../03_adr/ADR-019-wal-proprio-volume-dedicado.md), [ADR-034](../../03_adr/ADR-034-fsync-fatal-e-identidade-nova.md), [ADR-028](../../03_adr/ADR-028-timeouts-em-cascata.md), [ADR-045](../../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md), [ADR-047](../../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md), [ADR-048](../../03_adr/ADR-048-skip-por-item.md) · **Relacionados:** [X04 Partida e dois logs](../complexos/X04-partida-recuperacao-e-dois-logs.md)

## Cenário

Ao subir, o `DataReplication` valida configuração, abre os logs, completa o log de replicação a partir do Raft, retoma o applier até o último ponto aplicado e só então abre a porta de API. A parada é limpa: fecha a API, transfere a liderança se for a líder, drena e fecha os logs.

## Diagrama: partida e parada

```mermaid
flowchart TD
    A["Fx inicia"] --> B["config.Load valida env e YAML"]
    B -->|"inválida"| X["processo sai com erro"]
    B --> C["abre os logs e verifica CRC"]
    C -->|"corrupção no meio"| Y["recusa subir: reconstrução manual"]
    C -->|"cauda rasgada"| C2["trunca a cauda e segue"]
    C2 --> D
    C --> D["RaftNode inicia e completa o log de replicação a partir do Raft"]
    D --> E["Applier retoma pelo progresso por item até o high-water mark anterior"]
    E --> F["readyz verde"]
    F --> G["porta de API abre"]
    G --> H(("em serviço"))
    H -->|"SIGTERM"| I["readyz vermelho e fecha a API"]
    I --> J["transfere a liderança se for a líder"]
    J --> K["drena gate e applier"]
    K --> L["fecha Raft e logs"]
```

## Diagrama: estados do nó

```mermaid
stateDiagram-v2
    [*] --> Recuperando
    Recuperando --> Follower: logs ok e applier alcançou
    Recuperando --> Recusando: corrupção no meio do log
    Follower --> Candidato: timeout de eleição
    Candidato --> Lider: ganhou a eleição
    Candidato --> Follower: perdeu
    Lider --> ReconstruindoGate: assumiu
    ReconstruindoGate --> Lider: gate reconstruído
    Lider --> Follower: transferência ou perda de quórum
    Follower --> Drenando: SIGTERM
    Lider --> Drenando: SIGTERM após transferir
    Drenando --> [*]
    Recusando --> [*]
```

## Etapas

1. **Configuração:** `CELL_ID`, `DEGRADED_POLICY` (`accept` ou `reject`, sem valor padrão) são obrigatórias; com `accept`, a validade `DEGRADED_LEASE` (D) também. Não há variável de modo: `READ_MODE_DEFAULT`, `WRITE_MODE` ou `MAX_STALENESS` ainda definidas recusam a partida ([ADR-057](../../03_adr/ADR-057-um-unico-modo-strong-consistency.md)). Chaves desconhecidas no YAML são recusadas; prazos fora de ordem recusam a partida.
2. **Logs:** verifica o CRC; cauda rasgada é truncada; corrupção no meio faz o nó recusar subir.
3. **Completar o log de replicação:** se estiver atrás do índice commitado, é completado a partir do log do Raft antes de aceitar tráfego.
4. **Applier:** retoma pelo progresso **por item** `(índice, ordem)`, persistido no volume do log ([ADR-048](../../03_adr/ADR-048-skip-por-item.md)), até o high-water mark anterior; as leituras fortes só abrem depois (evita ler dado velho e ressuscitar DELETE). A regra é imposta por código e coberta por teste de propriedade ([ADR-047](../../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md), invariante `I-DEL`). Líder recém-eleita só atende leitura forte depois de reconstruir o gate ([C08](C08-queda-de-celula-lider-ou-az-aws.md)).
5. **Probes:** `startupProbe` cobre a recuperação; `readinessProbe` não depende de quórum; `livenessProbe` só diz se o loop trava.
6. **Parada:** SIGTERM fecha a API, transfere a liderança, drena com prazo menor que o `terminationGracePeriodSeconds` e fecha os logs.

## Falhas e tratativas

- **Tempo de recuperação longo.** Não quantificado: [R18](../a_rever/R18-rto-nao-quantificado.md).
- **Reinício durante o modo degradado.** O contador do lease (`t_c`) vive em memória e se perde. Um nó que volta e encontra `DEGRADE` sem `RESTORE` não herda `t_c`: trata o lease como expirado até obter contato com a Azure ou um `RENEW` ([ADR-044](../../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [C32](C32-particao-e-lease-do-modo-degradado.md)).

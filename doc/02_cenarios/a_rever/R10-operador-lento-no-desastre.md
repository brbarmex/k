# R10 — Tempo de decisão humana no desastre

**Status:** ❓ A rever · sem resolução · **ADRs:** [ADR-033](../../03_adr/ADR-033-promocao-da-azure-humana.md), [ADR-044](../../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md) · **Relacionados:** [C14](../atualizados/C14-aws-cai-sem-degradacao.md), [X03](../complexos/X03-desastre-e-promocao-da-azure.md)

## Cenário

Como a promoção da Azure é sempre humana (ADR-033), o RTO inclui **perceber, decidir e executar**. Sem plantão e critérios pré-aprovados, isso pode levar horas. Em instalações `accept` a decisão não pode ser mais rápida que o lease: `ForcePromote` recusa antes de D + M desde o último contato da AWS ([ADR-044](../../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md)), então o tempo humano corre em paralelo a essa espera, não depois dela. O RTO do projeto é `Delta-T` e ainda não tem número ([R18](R18-rto-nao-quantificado.md)).

## Diagrama: Linha do tempo do RTO com promoção

```mermaid
flowchart LR
    A["Falha da AWS"] --> B["Detectar: alertas externos"]
    B --> C["Decidir: critérios pré-aprovados"]
    C --> W["Esperar D + M se a instalação for accept"]
    W --> D["Executar: drctl promote com confirmação"]
    D --> E["Eleição entre Z1 Z2 Z3"]
    E --> F["Drenar backlog do Cosmos"]
    F --> G["Abrir escritas e depois leituras fortes"]
```

## Fluxo proposto

1. **Plantão 24x7** com runbook e critérios de decisão aprovados antes pelo negócio.
2. **Promoção manual automatizada:** um único comando (`drctl promote`) que faz toda a sequência depois da confirmação humana, com pré-checagens e registro de auditoria.
3. **Game days** periódicos medindo o tempo real de cada etapa.
4. Sinais externos independentes da AWS (monitoramento sintético a partir da Azure e de fora, status do provedor).

## Falhas e tratativas

- **Decisão tardia.** Sem limite de tempo, a decisão vira "esperar mais um pouco". Definir um prazo após o qual o plantão decide com a evidência disponível.

## Pontos em aberto

- Quem tem autoridade de decidir a promoção e qual o tempo máximo de decisão. Liga com a PEND-01, cujo desenho está decidido (ADR-044); a promoção automática segue com os outros arquitetos e não é reaberta. O prazo para decidir deve ser escolhido junto com D e M.

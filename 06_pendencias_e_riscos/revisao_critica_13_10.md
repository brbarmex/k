# Revisão crítica de 13/10/2026: achados e tratamento

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

O Bruno revisou o pacote e concluiu que a arquitetura é **candidata para prototipação, não para produção na forma atual**: o texto descreve bem o caminho feliz e a reação às falhas, mas ainda não prova os invariantes. O agente arquiteto concordou com o parecer, confirmou no código e nos ADRs os defeitos apontados e respondeu ponto a ponto em [analise_do_arquiteto_13_10.md](analise_do_arquiteto_13_10.md). O Bruno decidiu **a em todas as 14 decisões** (a 11 é "manter um banco por célula").

## Achados e tratamento

| # | Achado | Posição do arquiteto | Tratamento | Onde |
|---|---|---|---|---|
| 1 | Fencing e split-brain: duas pontas podem aceitar escrita | Concordo; o problema nasce da opção B | **Decidido:** modo degradado com lease D; a Azure só promove depois de D + M | [ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) |
| 2 | Prefixo do gate e reconstrução na troca de líder | Concordo em parte (o Raft já prova o prefixo dos learners) | **Decidido:** progresso só pelo Raft, no termo atual, gate por evento; TLA+ ampliado | [ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md), [X01](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md) |
| 3 | DELETE, TTL e replay sem prova | Concordo em parte (a ordem por chave converge; falta prova e proteção nos caminhos fora do checkpoint) | **Decidido:** manter DELETE real com invariante `I-DEL`; tombstone fora da v1 | [ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md), [X06](../02_cenarios/complexos/X06-delete-ttl-gsi.md) |
| 4 | Reconstrução sem protocolo | Concordo | **Decidido:** protocolo em tabela sombra com cutover e checksum | [ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md), [C33](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md) |
| 5 | `SKIP` de lote parcial | Concordo | **Decidido:** `SKIP` por item `(índice, ordem)` | [ADR-048](../03_adr/ADR-048-skip-por-item.md), [C26](../02_cenarios/atualizados/C26-entrada-venenosa.md) |
| 6 | RPO 0 condicional e header como aviso | Concordo | **Decidido:** `DEGRADED_POLICY` obrigatória por instalação | [ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md) |
| 7 | Compatibilidade com o DynamoDB | Concordo em parte (o recorte da API é decisão, falta inventário e suíte) | **Decidido:** inventário, levantamento de condicionais, suíte de conformidade, `_dr_v` fora das respostas | [ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md) |
| 8 | Leitura forte: `ReadIndex` e apply | Concordo; achei o problema pior (acopla a leitura à Azure) | **Decidido:** barreira `min(ReadIndex, gate)` | [ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md), [C05](../02_cenarios/atualizados/C05-leitura-strong-e-eventual.md) |
| 9 | GSI "forte" | Concordo | **Decidido:** `ConsistentRead=true` em GSI é recusada | [ADR-050](../03_adr/ADR-050-gsi-nao-tem-leitura-forte.md) |
| 10 | Idempotência de 5 min | Concordo em parte (o DynamoDB também não é idempotente por operação de negócio) | **Decidido:** documentar o limite e levantar o uso de escrita condicional | [ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md), [R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) |
| 11 | NetworkPolicy sem autenticação por aplicação | Concordo; é ampliação de privilégio, não só impersonação | **Decidido:** mTLS por aplicação com lista de permissões | [ADR-052](../03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md), [C34](../02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md) |
| 12 | Eventual: ADR-009 contra ADR-023 | Concordo, com mais ênfase | **Decidido:** o apply paralelo sai da v1; Eventual é só leitura | [ADR-053](../03_adr/ADR-053-eventual-sem-apply-paralelo-na-v1.md), [C04](../02_cenarios/atualizados/C04-escrita-eventual.md) |
| 13 | DynamoDB por célula não é DynamoDB por AZ | Concordo com o diagnóstico | **Decidido pelo Bruno:** manter um banco por célula | [ADR-054](../03_adr/ADR-054-banco-por-celula-mantido.md) |
| 14 | Oito defeitos no esboço de código | Concordo nos oito (conferidos) | **Corrigidos** no esboço, com testes de concorrência | [05_implementacao](../05_implementacao/README.md) |
| 15 | Seis discordâncias de documentação | Concordo nas seis | **Corrigidas:** matriz automático/assistido/manual, dedupe por requisição, definição de pronto, SLO ponta a ponta, RTO como distribuição medida | [Documento principal](../DataReplication_Versao_1.md), [08_resiliencia](../01_arquitetura/08_resiliencia_autocura_e_timeouts.md), [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md) |
| 16 | Backlog do Cosmos | Concordo (já era o R03) | Aberto: limite de lag de apply para elegibilidade ao failover | [R03](../02_cenarios/a_rever/R03-backlog-de-apply-no-cosmos.md), PEND-18 |
| 17 | Cinco fases de trabalho | Concordo, com um ajuste de ordem | **Adotadas:** TLA+ ampliado nasce junto da fase 1; núcleo mínimo verificável antes dos seis caminhos de banco | [Documento principal, seção 9](../DataReplication_Versao_1.md#9-implementação) |

## O que o relatório não pegou (acrescentado pelo arquiteto)

1. **Leitura forte acoplada à Azure** (item 8): corrigida pela barreira `min(ReadIndex, gate)`.
2. **Ampliação de privilégio pelo proxy** (item 11).
3. **Semântica do Cosmos depois do failover** (tamanho de item, precisão de `Number`, índices consistentes, TTL): entra na suíte de conformidade ([ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md), PEND-08).
4. **Relógio no lease:** depende de relógio monotônico, de checar a validade a cada ACK e de lidar com pausa de processo e salto de tempo ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), H-DR13 e H-DR14 em [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md)).
5. **Escrita condicional excluída:** para aplicações financeiras pode pesar mais que qualquer limite de latência (PEND-15).

## O que ainda depende de pessoas ou medição

- **PEND-02:** o negócio aprovar o RPO > 0 e o valor de D, que dimensiona o RTO no pior caso (D + M) e o log.
- Modelo TLA+ ampliado, que prova (ou refuta) o lease antes do código.
- Inventário real das chamadas e das permissões das aplicações; estimativa de custo por cliente (R20).

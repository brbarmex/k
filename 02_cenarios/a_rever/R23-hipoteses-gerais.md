# R23 — Hipóteses gerais em aberto

**Status:** ❓ A rever · sem resolução · **ADRs:** [ADR-030](../../03_adr/ADR-030-verificacao-formal-tla.md), [ADR-033](../../03_adr/ADR-033-promocao-da-azure-humana.md), [ADR-014](../../03_adr/ADR-014-modo-degradado-opcao-b.md), [ADR-032](../../03_adr/ADR-032-parse-json-completo-e-rust.md) · **Relacionados:** [C12](../atualizados/C12-modo-degradado.md), [X02](../complexos/X02-modo-degradado.md), [X03](../complexos/X03-desastre-e-promocao-da-azure.md)

## Cenário

Este arquivo reúne as hipóteses de falha que **não viraram um cenário próprio**: algumas já têm tratamento parcial, outras seguem sem solução. As que têm cenário dedicado aparecem na tabela com o link.

## Índice das hipóteses

| Id | Hipótese | Tratada em | Situação |
|---|---|---|---|
| H1 / PEND-01 | Split-brain na promoção com a AWS viva | [R12](R12-promocao-com-aws-viva.md) | Com os outros arquitetos; não reabrir |
| H2 / PEND-02 | RPO > 0 ilimitado no modo degradado | abaixo | **Bloqueante** |
| H3 / PEND-03 | Reconciliação de órfãs | [R13](R13-escritas-orfas-reconciliacao.md) | Sem resolução |
| H4 / PEND-11 | Líder lenta, mas viva | abaixo | Proposta |
| H5 | Duas AZs da AWS caem | [R06](R06-duas-azs-aws-caem.md) | Sem resolução |
| H6 | Tempo de decisão humana | [R10](R10-operador-lento-no-desastre.md) | Sem resolução |
| H7 | Corrupção lógica ou bug replicado | abaixo | Proposta |
| H8 | Bug no consenso customizado | abaixo | Mitigado por ADR-030 |
| H9 | Dependência de relógio nas leituras fortes | abaixo | Mitigado: ReadIndex |
| H10 | Plano de controle e identidade dos provedores | abaixo | Aberta |
| H11 | Shard maior que a capacidade de um log | abaixo | Aberta |
| H-DR1 | Reconstrução automática de réplica | abaixo | Manual assistida na v1 |
| H-DR2 | Desvio automático de entrada venenosa | [C26](../atualizados/C26-entrada-venenosa.md) | Decidido: humano (ADR-038) |
| H-DR3 | Nome do atributo de versão | [R17](R17-atributo-de-versao-e-limite-400kb.md) | Aberta |
| H-DR4 | Chave quente que satura um shard | abaixo | Medir |
| H-DR5 | Learner Azure lento, mas vivo | [C25](../atualizados/C25-link-lento-faixa-cinzenta.md) | Só alerta (ADR-037) |
| H-DR6 | Parse completo do JSON no caminho quente | abaixo | **Aceita** pelo Bruno |

## H2 / PEND-02. RPO > 0 sem teto no modo degradado (bloqueante)

**Explicação.** A perda possível cresce com a duração da degradação. Com o link fora por dias, uma queda da AWS perde dias de transações já confirmadas. O header `x-dr-mode: degraded` informa o cliente, mas não limita a perda. O Bruno decidiu pela opção B ([ADR-014](../../03_adr/ADR-014-modo-degradado-opcao-b.md)); falta o **aceite formal** do negócio.

**Caminhos:**
- Aceite formal com um **teto**: passadas X horas em degradado, uma pessoa decide entre seguir ou parar.
- Segundo circuito privado por outra operadora, para degradar menos ([C25](../atualizados/C25-link-lento-faixa-cinzenta.md)).
- Exportação periódica do log degradado para um armazenamento que a Azure consiga ler depois (reduz a perda, não elimina; [R13](R13-escritas-orfas-reconciliacao.md)).

## H4 / PEND-11. Líder lenta, mas viva

**Explicação.** Uma líder com disco ou rede degradados continua mandando heartbeat e não perde a eleição, mas deixa todas as escritas lentas.

```mermaid
flowchart TD
    A["Métricas da líder: P99 de fsync, latência de commit, atraso de apply"] --> B{"Acima do limite por N segundos?"}
    B -->|"não"| C["Segue como líder"]
    B -->|"sim"| D{"Cooldown cumprido e outra célula saudável?"}
    D -->|"sim"| E["Transferência planejada de liderança: ADR-013"]
    D -->|"não"| F["Alerta e mantém, para não oscilar"]
```

**Caminho:** critério de saúde além do heartbeat, com histerese e cooldown, disparando a transferência planejada ([ADR-013](../../03_adr/ADR-013-transferencia-planejada-de-lideranca.md)). Falha de fsync continua sendo fatal ([ADR-034](../../03_adr/ADR-034-fsync-fatal-e-identidade-nova.md)); o watchdog cobre a lentidão sem erro.

## H7. Corrupção lógica ou bug replicado

**Explicação.** RPO 0 replica tudo, inclusive erros. Um deploy com bug que grava dado errado é replicado na hora para as duas clouds.

**Caminhos:** backup contínuo dos dois bancos (PITR no DynamoDB, backup contínuo no Cosmos); retenção do log suficiente para reconstruir o estado até um índice anterior ao erro; validação de esquema e de capacidades antes do commit ([C26](../atualizados/C26-entrada-venenosa.md)).

## H8. Bug no consenso customizado

**Explicação.** A regra "eleição só na AWS + gate com maioria Azure + degradação" não é Raft padrão. Um erro sutil pode perder dado ou causar split-brain sem falha de infraestrutura.

**Tratamento:** modelo TLA+ **antes** do código do gate ([ADR-030](../../03_adr/ADR-030-verificacao-formal-tla.md), [X01](../complexos/X01-gate-e-troca-de-lider.md)) e testes tipo Jepsen (partições, pausas, relógios defasados). O Raft em si é o `etcd-io/raft`, mantido fora do código nosso.

## H9. Dependência de relógio nas leituras fortes

**Explicação.** Leituras por lease confiam que o relógio dos nós não deriva além de um limite.

**Tratamento:** a leitura forte usa **ReadIndex**, não lease ([R16](R16-leitura-forte-em-celulas-follower.md)). Se um dia lease for usado, monitorar o desvio de relógio e desligar acima de um limite.

## H10. Plano de controle e identidade dos provedores

**Explicação.** Incidentes globais costumam ser de identidade ou DNS, não de dados. Se o acesso ao Cosmos ou ao DynamoDB depender de emitir um token no momento da escrita, o caminho de dados para com os bancos de pé.

**Caminhos:** credenciais com rotação controlada e tokens de validade longa em cache; testar a camada com o serviço de identidade do provedor indisponível ([R11](R11-api-de-promocao-depende-da-aws.md), [C31](../atualizados/C31-implantacao-por-cliente-e-identidade.md)).

## H11. Crescimento de um shard além da capacidade de um log

**Explicação.** Com ordenação por shard, cada shard tem uma líder e um log. Se um segmento crescer muito além de 3.000 TPS, não há procedimento de divisão de shard.

**Caminhos:** desenhar o split de shard (novo grupo de consenso, cópia, troca de roteamento) ou evoluir para ordenação por chave de partição com apply paralelo por chave.

## H-DR1. Reconstrução automática de réplica

**Explicação.** Quando uma réplica perde o disco ou fica fora da janela de retenção, a v1 usa `drctl rebuild`, **manual assistido** ([C21](../atualizados/C21-disco-fsync-e-reconstrucao.md)). A automação completa (copiar de outra célula, validar, entrar como learner) fica em aberto e é a exceção à meta "recupera sem humano" (junto com PEND-01).

## H-DR4. Chave quente

**Explicação.** Muitas escritas à mesma chave serializam na fila por chave do applier e podem estourar throttling por partição no DynamoDB.

**Caminhos:** backpressure por chave e por cliente com os erros do DynamoDB que o SDK já entende ([ADR-025](../../03_adr/ADR-025-escopo-da-api-v1-e-sdks.md)); medir no teste de carga.

## H-DR6. Parse completo do JSON (aceita)

**Decisão do Bruno (12/10):** o custo de CPU e alocação do parse completo é **conhecido e aceito** ([ADR-032](../../03_adr/ADR-032-parse-json-completo-e-rust.md)). Medir no spike S2; revisitar se heartbeat ou P99 forem afetados. Ideia adiada: guardar o corpo em bytes no log e varrer só o nome da tabela.

## Pontos em aberto

- Decisões que dependem do Bruno: PEND-02 (aceite formal do RPO > 0, **bloqueante**) e as respostas dos outros arquitetos para PEND-01.

# R22 — Memória da janela de dedupe

**Status:** 🟡 A rever · limite da idempotência documentado em 13/10 ([ADR-051](../../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)); falta medir a memória e confirmar o id estável por SDK · **ADRs:** [ADR-026](../../03_adr/ADR-026-dedupe-de-retry-id-estavel.md), [ADR-028](../../03_adr/ADR-028-timeouts-em-cascata.md), [ADR-051](../../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md) · **Relacionados:** [C07](../atualizados/C07-timeout-depois-do-commit.md), [C19](../atualizados/C19-falha-do-datareplication.md), [X05](../complexos/X05-timeouts-e-idempotencia.md)

## Cenário

A janela de dedupe de 5 minutos precisa lembrar, na líder, o identificador estável de cada **requisição** recente (o dedupe é por requisição, não por item; um `BatchWriteItem` é uma requisição). A 3.000 TPS são **~900 mil identificadores** na janela (3.000 × 300 s). Com ~64 bytes por registro (id, índice, instante, estado), são **~58 MiB por shard**, que não estavam no orçamento de memória do pod.

**Efeito escondido:** a tabela de dedupe tem de existir **na líder nova** quando ela assume. Se só a líder a mantém, a nova líder reconstrói lendo 5 minutos de log (~1,8 GB a 2 KB por entrada) durante a eleição, inflando o RTO.

## Diagrama: Tabela mantida por todos os votantes

```mermaid
flowchart TD
    A["Entrada commitada no log"] --> B["CommitPump entrega a entrada"]
    B --> C["Todos os votantes atualizam a tabela de dedupe: id, índice, instante"]
    C --> D["Expiração por janela de 5 min"]
    E["Líder nova assume"] --> F["Já tem a tabela: sem leitura de 5 min de log"]
    G["Reinício de um nó"] --> H["Reconstrói pelo trecho recente do log do Raft"]
    H --> I{"Retenção do log do Raft cobre a janela?"}
    I -->|"não"| J["Parte compactada vem do snapshot (ADR-063, pendente)"]
    I -->|"sim"| K["Pronto"]
```

## Fluxo proposto

1. A tabela é uma **projeção do log**: todo votante a atualiza ao aplicar o commit, então qualquer um pode virar líder sem reconstruir.
2. A reconstrução no reinício lê o trecho recente do **log do Raft**, cuja retenção mínima cobre a janela de dedupe mais uma margem ([04_wal_e_fsync](../../01_arquitetura/04_wal_e_fsync.md), 6.9). Na POC a compactação está desligada e o log inteiro está disponível; em produção, o que tiver sido compactado vem do snapshot ([ADR-063](../../03_adr/ADR-063-snapshot-e-compactacao-do-log-do-raft.md), pendente).
3. Incluir ~58 MiB (e a variação com o tamanho do id) no orçamento de memória e em `GOMEMLIMIT`; estrutura compacta (tabela hash com id de 16 a 36 bytes) e expiração em lotes.
4. Janela configurável (`DEDUPE_WINDOW`), com métrica de ocupação e de acertos.

## Falhas e tratativas

- **ABA fora da janela (limite da idempotência, documentado no [ADR-051](../../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)).** Retry da **aplicação** acima do SDK, depois de mais de 5 min, escapa do dedupe. Para um `PutItem`, isso pode sobrescrever uma escrita mais nova da mesma chave (A, depois B por outro cliente, depois a repetição de A). Isso acontece **também no DynamoDB**: a camada não piora a situação além da janela de dedupe, e fora dela vale a semântica do DynamoDB. O defeito de fundo é outro: a escrita condicional está fora da v1, então uma aplicação com concorrência otimista (versão, saldo) não tem como se proteger pela camada. Por isso, antes de cada cliente entrar, levanta-se o uso real de `ConditionExpression` (PEND-15); um identificador durável de operação de negócio seria extensão fora do contrato DynamoDB e não foi adotado. Confirmar com os times se alguma aplicação retenta por cima do SDK.
- **Id estável.** O dedupe depende de um identificador estável por SDK (por exemplo `amz-sdk-invocation-id`). **Verificar em cada SDK** (Java 2.x, Go v2, .NET v4) que o valor é o mesmo entre as tentativas; hash do conteúdo é só o último recurso e causa falso positivo para escritas idênticas legítimas ([ADR-026](../../03_adr/ADR-026-dedupe-de-retry-id-estavel.md)).

## Pontos em aberto

- Confirmar o cabeçalho de identificador estável em cada SDK (suíte de conformidade, PEND-16) e se alguma aplicação retenta por cima do SDK; levantar o uso de `ConditionExpression` por cliente (PEND-15).
- Retenção do log do Raft e tamanho da janela depois do spike.

# Resiliência, autocura e orçamento de tempo

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 9. Resiliência e autocura

### 9.1 Matriz de falhas do próprio processo

| Falha | Comportamento esperado |
|---|---|
| Pod do `DataReplication` reinicia | As apps da célula recebem erro de conexão e o SDK tenta de novo até ele voltar; o Compass-Router (externo) pode desviar o tráfego. |
| Processo reinicia | Recupera o WAL, volta como follower, alcança o log. Se era líder, há eleição. |
| Perde o disco | Volta com **identidade nova** como learner e reconstrói (6.6). **Nunca** reusar o ID antigo com log vazio: o nó pode já ter votado e confirmado entradas, e isso viola a segurança do Raft. |
| Corrupção no meio do WAL | Recusa subir; reconstrução como acima. |
| Falha de fsync | Encerra o processo (sem retry). |
| Disco lento (stall) | *Watchdog* mede a latência de fsync; acima do limiar por N segundos, a líder **transfere a liderança** (critério de "líder degradada" que o ADR-010 deixou pendente). |
| Link AWS↔Azure cai | Depende de `DEGRADED_POLICY` ([ADR-045](../03_adr/ADR-045-politica-de-degradacao-por-instalacao.md)). **`accept`:** modo degradado (opção B) via entrada replicada `DEGRADE`, **com lease de duração D** (5.3.2). **`reject`:** a líder nunca degrada; sem o gate da Azure a escrita não é ACKada e o cliente recebe `503 PENDING` (RPO 0 sempre). |
| Lease do modo degradado expira (`accept`) | A líder confere a validade a cada ACK; passado D, a AWS recusa escrita com **`503 LEASE_EXPIRED`**. Leitura e Raft seguem. Só uma pessoa prorroga, por `RENEW` replicado ([ADR-044](../03_adr/ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), [C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md)). |
| Entrada venenosa (o applier falha sempre) | Trava o applier do shard. Na v1: contador de falhas por **item**, alarme (`poison_entry_index`) e **parada explícita do shard** com marcador visível. A **leitura forte do shard falha rápido com `503`** em vez de esperar o timeout ([ADR-049](../03_adr/ADR-049-barreira-da-leitura-forte-e-o-gate.md)). O `SKIP` identifica o item `(índice, ordem)`, é sempre humano e o progresso é persistido por item ([ADR-048](../03_adr/ADR-048-skip-por-item.md)). Desvio automático fica como hipótese aberta (H-DR2). |
| Applier ou banco da célula atrasado (throttling) | Backoff limitado; `last_applied` fica para trás do gate e o lag é alertado. A leitura forte espera `min(ReadIndex, gate)` e a leitura eventual informa o atraso. |
| Rajada de carga | Backpressure (9.3). |

### 9.2 Probes e ciclo de vida no Kubernetes
- **startupProbe**: tempo de recuperação do WAL pode ser longo; sem isso o kubelet mata o pod no meio.
- **readinessProbe**: "posso atender" (WAL recuperado, conexão com o banco da célula). **Não** depende de ter quórum, senão a perda de uma célula tira todas do balanceamento.
- **livenessProbe**: só "o processo está travado?" (loop principal responde). **Nunca** ligada ao estado do cluster: causa reinício em cascata e piora o incidente.
- **Ordem de partida:** as apps toleram o `DataReplication` ainda indisponível (retry do SDK). Sem sidecar nativo nesta versão.
- **Desligamento limpo** (SIGTERM): para de aceitar, drena com prazo menor que `terminationGracePeriodSeconds`, e, se for líder, **transfere a liderança** antes de sair ([ADR-013](../03_adr/ADR-013-transferencia-planejada-de-lideranca.md)).
- **PodDisruptionBudget** com `maxUnavailable: 1` por grupo de réplicas, alinhado com "uma célula por vez". `topologySpreadConstraints` por AZ. Cuidado com *autoscaler* de nós (Karpenter/Cluster Autoscaler) que drena nós ignorando o desenho: respeitar o PDB.

### 9.3 Backpressure e proteção
- Filas limitadas por bytes e por tempo. Cada requisição carrega o *deadline* restante do orçamento; quem já estourou é descartada **sem trabalho**.
- Quando sobrecarregado, o `DataReplication` responde com os erros do DynamoDB que o SDK já sabe tratar (`ThrottlingException`, `ProvisionedThroughputExceededException`), em vez de inventar erros novos.
- **Atenção:** o retry do SDK gera um `commandId` novo. Isso é o PEND-13. **Aprovado (Bruno, 12/10; D11):** **dedupe no líder** usando identificador estável do SDK quando existir e hash (tabela, chave, conteúdo) só como último recurso; a janela é de 5 min e o dedupe é **por requisição inteira** ([ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md)), não por item; o resultado de um lote é consultado **por item** (`APPLIED`, `REJECTED`, `PENDING`, ADR-048). Timeout do SDK maior que o deadline do DataReplication. **Limite documentado ([ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)):** fora da janela vale a semântica do DynamoDB (um `PutItem` cego repetido depois de 5 min pode sobrescrever uma versão mais nova, também no DynamoDB), e não há identificador durável de operação de negócio na v1. A tabela de dedupe é derivada do log (todas as réplicas a reconstroem igual) e reconstruída na partida.
- `PENDING` (resultado desconhecido) deve ser mapeado para um erro que o SDK **retente** com segurança; a escrita é idempotente graças à versão (6.4) **dentro da janela de dedupe**. Com `DEGRADED_POLICY=reject`, uma queda da Azure ou do link produz `503 PENDING` nas escritas da instalação.

---

### 9.4 Orçamento de tempo: prazos em cascata dentro dos 30 s do SDK (Bruno, 12/10)

**Regra:** cada etapa tem um prazo **menor** que o da etapa que a chama, sempre partindo do **menor timeout entre os SDKs dos clientes** (30 s, `CLIENT_TIMEOUT`). O `DataReplication` precisa **responder, com sucesso ou com erro definido, antes de o SDK desistir**. Uma requisição que o servidor ainda segura depois do timeout do cliente é trabalho perdido e vira retry com risco de duplicação (PEND-13, ADR-026).

**Os 30 s são por tentativa (Bruno, 12/10).** Cada tentativa do SDK espera até 30 s e, se não receber resposta, retenta, então a chamada inteira pode durar bem mais que 30 s. Responder cedo importa por dois motivos: o servidor não segura conexões e recursos por 30 s à toa, e um erro rápido deixa o SDK retentar logo. Por isso o prazo da API é `REQUEST_DEADLINE = CLIENT_TIMEOUT / 4 = 7,5 s`. O SDK não envia o seu timeout ao servidor, então **o valor é configuração**, e a partida valida a ordem dos prazos (7.6 do guia de implementação). `CLIENT_TIMEOUT` passa a significar o **timeout por tentativa**.

| Camada (de fora para dentro) | Prazo proposto | Observação |
|---|---|---|
| Timeout do SDK do cliente | **30 s** | Referência (por tentativa; menor entre Java, Go e .NET) |
| `REQUEST_DEADLINE` (API) | **7,5 s** | Prazo único da requisição; vira o `context` que desce por todas as camadas |
| Forward para a líder (gRPC) | restante do prazo, no máximo 3 s | Deadline gRPC herdado do contexto |
| Eleição + reconstrução do gate (D9) | ≤ 3 a 4 s | Falha de líder no meio da requisição precisa caber no prazo |
| `DEGRADE_AFTER` (espera pela Azure antes de propor DEGRADE) | **2 s** | **Tem de ser menor que o prazo da API**; senão toda escrita estoura durante uma queda da Azure antes de o modo degradado começar. Só existe com `DEGRADED_POLICY=accept` |
| `DEGRADED_LEASE` (D), validade do modo degradado | **a definir** (PEND-02) | **Não é prazo de requisição:** é o teto de duração do RPO > 0, contado no relógio monotônico da líder desde o último contato com a maioria Azure (`t_c`), não desde a proposta do `DEGRADE`. Passado D, `503 LEASE_EXPIRED` |
| Margem M para promover (deriva de relógio + pausa de processo) | **a definir** | A Azure só é promovida depois de **D + M** desde o último contato da AWS; a API `ForcePromote` recusa antes disso |
| Cliente do DynamoDB/Cosmos no applier | tentativa 1 s, no máximo 2 tentativas | O SDK da AWS **dentro do** `DataReplication` tem retry próprio: limitar, senão o backoff consome o prazo (throttling) |
| Margem de resposta | ≥ 500 ms | Para serializar e devolver |

**O que acontece quando o prazo estoura:** se a entrada **já foi commitada** (durável) e só o gate ou o apply (Strong) não terminou, a resposta é `PENDING` (erro retentável, 503). A escrita **não se perde**: o applier termina depois, e o retry do SDK é absorvido pelo dedupe (D11) e pela versão condicional (6.4). Se ainda **não foi commitada**, o erro é igual, e o retry é uma escrita nova normal.

**Consequências que este orçamento impõe:**
1. **O prazo de 40 ms do exemplo anterior era a meta de latência (P99), não um timeout.** Prazo e meta são coisas distintas: a meta mede, o prazo protege.
2. **Janela de dedupe** (D11): clientes usam **no máximo 3 tentativas de 30 s** (Bruno, 12/10). Pior caso, com o servidor mudo: 3 × 30 s + backoff ≈ 90 a 130 s. Com o servidor respondendo em até 7,5 s, a chamada inteira termina em torno de 25 s. A janela cobre o pior caso com folga: **5 min**. Retry da própria aplicação por cima do SDK não está coberto; se existir, a janela precisa crescer.
3. **`IdleTimeout` do servidor HTTP** maior que o idle das conexões dos SDKs (Java 60 s, Go 90 s): senão o SDK reutiliza uma conexão que o servidor já fechou, recebe reset e retenta sem necessidade. Fixado em 120 s.
4. **Padrões de timeout diferem por SDK.** Todos os clientes devem fixar `apiCallTimeout`/`apiCallAttemptTimeout` (ou equivalente) **explicitamente** com a mesma política, para o orçamento valer.
5. **O lease é outra escala de tempo.** D e M são da ordem de minutos ou mais (valor a definir), não de segundos; eles não entram na cascata de 30 s. O que entra é o efeito: depois de D, a resposta imediata é `503 LEASE_EXPIRED` (erro definido, dentro do prazo).
6. **Teste de aceitação:** em cada cenário de falha (matar a líder, cortar o link AWS↔Azure, throttling do DynamoDB, disco lento), **nenhuma requisição ultrapassa `REQUEST_DEADLINE` + margem**, e o resultado é sucesso ou erro definido. Entra na suíte de falhas (seção 13), nos dois valores de `DEGRADED_POLICY`.

---

### 9.5 Matriz automático, assistido e manual (decisão 13, 13/10)

A definição de pronto fala em recuperação sem humano para falhas de uma célula, mas várias ações são humanas por decisão (promoção, `SKIP`, `drctl rebuild`, órfãs). A matriz diz **quem age** em cada situação:

- **Automático:** o sistema detecta e corrige sozinho.
- **Assistido:** o sistema detecta, prepara, executa os passos e **recusa o que for inseguro**, mas só avança com a **aprovação de uma pessoa**.
- **Manual:** uma pessoa decide e age; o sistema só informa e registra.

| Situação de falha ou operação | Quem age | O que o sistema faz | O que a pessoa faz |
|---|---|---|---|
| Processo ou pod reinicia, falha de fsync (o processo encerra) | **Automático** | Recupera o WAL, volta como follower, alcança o log | Nada |
| Queda de uma célula ou líder | **Automático** | Eleição, líder nova reconstrói o gate, janela curta sem ACK | Nada |
| Disco lento (stall) | **Automático** | Watchdog de fsync; a líder transfere a liderança | Troca o volume se persistir |
| Azure ou link inalcançável por `DEGRADE_AFTER` (`accept`) | **Automático** | Propõe `DEGRADE` com lease D, marca o header e a métrica | Decidiu antes: `DEGRADED_POLICY`, D e o aceite do RPO > 0 |
| Azure ou link inalcançável (`reject`) | **Automático** | Não degrada; `503 PENDING` nas escritas | Decidiu antes a política |
| Lease expirado | **Automático** | A AWS recusa escrita (`503 LEASE_EXPIRED`); leitura e Raft seguem | Ver prorrogação |
| Azure volta, ressincronização e `RESTORE` | **Automático** | Catch-up, histerese, `RESTORE` | Nada, se dentro da janela de retenção |
| Throttling do banco, applier atrasado | **Automático** | Backoff limitado, alerta de lag | Ajusta a capacidade |
| SDK não suportado | **Automático** | Rejeita com 400, métrica | Atualiza o cliente |
| Cliente sem entrada na lista de permissões mTLS | **Automático** | Responde `403` ([ADR-052](../03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md)) | Cadastra a aplicação na lista (manual, ver abaixo) |
| **Prorrogação do lease (`RENEW`)** | **Assistido** | Mostra o estado da Azure, replica o `RENEW` no Raft da AWS, recusa se a Azure já pode ser promovida (H-DR13) | Aprova a prorrogação |
| **Promoção da Azure (`ForcePromote`)** | **Assistido** | A API **recusa antes de D + M**; depois, apresenta o checklist de evidências e cria a nova epoch | Decide e aciona ([ADR-033](../03_adr/ADR-033-promocao-da-azure-humana.md)) |
| **Entrada venenosa e `SKIP`** | **Assistido** | Detecta, para o shard com marcador, informa o item `(índice, ordem)`, executa o `SKIP` auditado, leitura forte falha rápido | Aprova o `SKIP` do item ([ADR-048](../03_adr/ADR-048-skip-por-item.md)) |
| **`drctl rebuild` (tabela sombra)** | **Assistido** | Conduz F, varredura, replay, verificação por checksum; falha se o log não estiver retido desde F | Aprova o cutover ([ADR-047](../03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)) |
| **Perda de disco, corrupção do WAL** | **Assistido** | Recusa subir ou sobe com identidade nova como learner e inicia a reconstrução | Aprova a reconstrução e a troca do volume |
| **Extração de órfãs (`drctl orphans export`)** | **Assistido** | Lê o log da AWS desde o `fromIndex` e gera o relatório por chave, separando "sem conflito" de "conflito" | Decide o que reaplicar ([R13](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md)) |
| **Recriação da célula AWS depois da promoção** | **Assistido** | Boot cercado, credenciais revogadas, recria o banco a partir da réplica ([ADR-042](../03_adr/ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md)) | Aprova a recriação (que apaga as órfãs) depois de extraí-las, com PITR como rede de segurança |
| **Failback (retorno à AWS)** | **Assistido** | Transferência planejada depois da célula reconstruída e validada | Aprova a data e a janela ([C16](../02_cenarios/atualizados/C16-aws-volta-apos-promocao.md)) |
| Aceite de D, M e do RPO > 0 (PEND-02); escolha de `DEGRADED_POLICY` | **Manual** | Valida a configuração na partida e a registra na auditoria | Negócio e risco decidem, por instalação |
| Lista de permissões por aplicação, emissão, renovação e revogação de certificados | **Manual** | Aplica a lista, recarrega certificados, registra | Mantém a lista e a operação dos certificados ([C34](../02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md)) |
| Reaplicação das escritas órfãs e comunicação ao cliente | **Manual** | Registra como escrita nova e auditada | Decisão de negócio caso a caso |
| Inventário de chamadas do cliente e conformidade antes de entrar | **Manual** | Roda a suíte de conformidade | Levanta o uso real e aprova a entrada ([ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)) |

**Consequência para a definição de pronto:** "recuperação sem humano" vale só para as linhas **automáticas**. Os tempos das linhas assistidas incluem a decisão humana (hipótese H6) e entram no RTO medido em exercícios ([06](06_modos_de_escrita_e_leitura_e_latencia.md), seção 7). Com `DEGRADED_POLICY=accept`, se a AWS morre em modo degradado, o RTO tem piso **D + M**.

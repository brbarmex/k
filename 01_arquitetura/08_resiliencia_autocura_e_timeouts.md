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
| Link AWS↔Azure cai | Opção B: modo degradado via entrada replicada (5.3). |
| Entrada venenosa (o applier falha sempre) | Hoje trava o shard. Na v1: contador de falhas por entrada, alarme e **parada explícita do shard** com marcador visível. Desvio automático fica como hipótese aberta (H-DR2). |
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
- **Atenção:** o retry do SDK gera um `commandId` novo. Isso é o PEND-13. **Aprovado (Bruno, 12/10; D11):** **dedupe no líder** usando identificador estável do SDK quando existir e hash (tabela, chave, conteúdo) só como último recurso; falta definir tamanho e expiração da janela e o tratamento item a item no BatchWriteItem. Timeout do SDK maior que o deadline do DataReplication. A tabela de dedupe é derivada do log (todas as réplicas a reconstroem igual) e reconstruída na partida.
- `PENDING` (resultado desconhecido) deve ser mapeado para um erro que o SDK **retente** com segurança; a escrita é idempotente graças à versão (6.4).

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
| `DEGRADE_AFTER` (espera pela Azure antes de propor DEGRADE) | **2 s** | **Tem de ser menor que o prazo da API**; senão toda escrita estoura durante uma queda da Azure antes de o modo degradado começar |
| Cliente do DynamoDB/Cosmos no applier | tentativa 1 s, no máximo 2 tentativas | O SDK da AWS **dentro do** `DataReplication` tem retry próprio: limitar, senão o backoff consome o prazo (throttling) |
| Margem de resposta | ≥ 500 ms | Para serializar e devolver |

**O que acontece quando o prazo estoura:** se a entrada **já foi commitada** (durável) e só o gate ou o apply (Strong) não terminou, a resposta é `PENDING` (erro retentável, 503). A escrita **não se perde**: o applier termina depois, e o retry do SDK é absorvido pelo dedupe (D11) e pela versão condicional (6.4). Se ainda **não foi commitada**, o erro é igual, e o retry é uma escrita nova normal.

**Consequências que este orçamento impõe:**
1. **O prazo de 40 ms do exemplo anterior era a meta de latência (P99), não um timeout.** Prazo e meta são coisas distintas: a meta mede, o prazo protege.
2. **Janela de dedupe** (D11): clientes usam **no máximo 3 tentativas de 30 s** (Bruno, 12/10). Pior caso, com o servidor mudo: 3 × 30 s + backoff ≈ 90 a 130 s. Com o servidor respondendo em até 7,5 s, a chamada inteira termina em torno de 25 s. A janela cobre o pior caso com folga: **5 min**. Retry da própria aplicação por cima do SDK não está coberto; se existir, a janela precisa crescer.
3. **`IdleTimeout` do servidor HTTP** maior que o idle das conexões dos SDKs (Java 60 s, Go 90 s): senão o SDK reutiliza uma conexão que o servidor já fechou, recebe reset e retenta sem necessidade. Fixado em 120 s.
4. **Padrões de timeout diferem por SDK.** Todos os clientes devem fixar `apiCallTimeout`/`apiCallAttemptTimeout` (ou equivalente) **explicitamente** com a mesma política, para o orçamento valer.
5. **Teste de aceitação:** em cada cenário de falha (matar a líder, cortar o link AWS↔Azure, throttling do DynamoDB, disco lento), **nenhuma requisição ultrapassa `REQUEST_DEADLINE` + margem**, e o resultado é sucesso ou erro definido. Entra na suíte de falhas (seção 13).

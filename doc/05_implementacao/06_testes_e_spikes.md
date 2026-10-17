# Testes de sistema e plano de spikes

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice da implementação](README.md)

## 13. Como vamos testar (resumo)

| Tipo | O que | Ferramenta/abordagem |
|---|---|---|
| Simulação determinística | Relógio, rede e disco falsos; semente reproduzível; injeção de partição, atraso, reordenação, perda e escrita incompleta | Harness próprio. Os testes de interação do próprio `etcd-io/raft` são uma boa base. `testing/synctest` ajuda. |
| Verificação formal | TLA+ ampliado: gate por termo, lease do modo degradado, promoção entre nuvens, troca de membros, dedupe, resultado desconhecido, DELETE e replay ([ADR-046](../03_adr/ADR-046-gate-so-conta-progresso-validado-pelo-raft.md)); hipóteses H-DR13 e H-DR14 | TLC/Apalache |
| Linearizabilidade | Histórico de operações contra um modelo | Porcupine (estilo Jepsen) |
| Concorrência | Todas as estruturas compartilhadas do núcleo (`Gate`, `Lease`, `DegradeService`, `CommitPump`) com goroutines em paralelo | `go test -race`, no CI de todo pull request |
| Lease | Relógio injetável: lease vence em `t_c + D` (último contato com a maioria Azure), proposta tardia com `t_c` antigo nasce vencida, líder nova ou nó reiniciado sem `t_c` fica vencido até contato ou `RENEW`, conferido a cada ACK com pausa de processo simulada, salto de relógio, `RENEW` e `RESTORE`; `reject` nunca degrada | Testes do `usecase` e do `domain`; relógio falso no harness de simulação; game day de partição ([C32](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md)) |
| Gate por evento | Progresso de learner libera o gate sem commit novo; `Append` lento e applier parado não atrasam o gate; termo antigo é ignorado | Testes do `CommitPump`; simulação com troca de líder |
| `I-DEL` por propriedade | Sequências aleatórias de Put e Delete com queda em qualquer ponto do replay convergem para o mesmo estado; rebuild em tabela sombra converge e o checksum por faixa fecha | Teste de propriedade (`testing/quick` ou `rapid`), `failpoints`, [C33](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md) |
| SKIP por item | Pula só `(índice, ordem)`; replay reaplica os anteriores; consulta devolve `APPLIED`, `REJECTED`, `PENDING` | Teste do `Applier` com queda entre itens; [C26](../02_cenarios/atualizados/C26-entrada-venenosa.md) |
| Conformidade DynamoDB x camada x Cosmos | A mesma sequência contra o DynamoDB real, a camada e o Cosmos pelo Cloud-Adapter, comparando erros, paginação, tamanhos, tipos numéricos, TTL e a ausência de `_dr_v` ([ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md), PEND-16) | `test/conformance`, com histórico de resultados por versão |
| mTLS por aplicação | Sem certificado a conexão é recusada; identidade fora da lista ou fora da tabela e da operação recebe `403`; renovação e revogação de certificado ([ADR-052](../03_adr/ADR-052-autorizacao-por-aplicacao-com-mtls.md), [C34](../02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md)) | Integração com os SDKs Java, Go e .NET; cert-manager de teste |
| RTO e SLO ponta a ponta | RTO como distribuição medida em exercícios de falha (mediana, P95, pior caso), com piso D + M se a AWS morre em degradado; SLO de latência, erro e recuperação medido sobre a operação completa | Game days repetidos; não é soma de P99 por etapa ([R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md)) |
| Nada sai antes do fsync (ADR-056) | O laço do `Ready` só envia respostas depois do `Save` com fsync; erro de fsync não envia nenhuma; no modo síncrono, só `MsgApp` da líder sai antes do fsync | Testes de `domain.CanSendBeforeSync`; disco falso no harness de simulação com queda entre `write` e `fsync` |
| Falhas no WAL | Queda em cada ponto da escrita, CRC corrompido, sufixo truncado, fsync com erro | *Failpoints*; `dm-flakey`/`dm-log-writes` em teste de integração |
| Fuzz | Decodificação do WAL, do protocolo gRPC e do JSON do DynamoDB | `go test -fuzz` |
| Caos em cluster | Matar pod, particionar AWS↔Azure, atrasar disco, esgotar disco | Chaos Mesh/LitmusChaos; AWS FIS e Azure Chaos Studio |
| Contrato | Cada SDK/versão contra o endpoint local | Suíte de compatibilidade (X3) |
| Desempenho | Carga a 3.000 TPS com tamanhos reais; medir P99.9 por etapa | Teste de carga com os discos e o link **reais** |

---

## 14. Plano: spikes antes de congelar o desenho

| Spike | Pergunta a responder | Critério de saída |
|---|---|---|
| S1. Medições de infraestrutura | RTT AWS↔Azure (P50/P99), fsync P99 em EBS e em Azure Disk, escrita condicional no DynamoDB (P99 com a cauda) e o custo de ReadIndex em lote; **IOPS de escrita e fsync P99 com 1, 4, 8 e 16 WALs do etcd em paralelo no mesmo volume, a 3.000 e a 6.000 TPS** ([ADR-056](../03_adr/ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)) | Números reais para o orçamento da seção 7, separando `t_parse`, `t_raft`, `t_azure` e `t_apply` ([C24](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md), [R16](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md)); teto de shards por nó e IOPS a provisionar (PEND-23). **Gatilho do WAL compartilhado:** na carga de 2x, fsync P99 acima do orçamento ou IOPS acima de 70% do provisionado |
| S1b. Síncrono com F3 x `AsyncStorageWrites` | O `AsyncStorageWrites` do `etcd-io/raft` melhora o P99 do commit ou a robustez a fsync lento em relação ao modo síncrono com F3, no volume real, com 1 shard e com k shards? | Adota o assíncrono se o P99 do commit cair pelo menos 20% **ou** se as eleições causadas por fsync lento (atraso injetado de 50 a 500 ms na líder) deixarem de acontecer; senão fica o síncrono. Nos dois modos: nenhuma resposta antes do fsync (teste do risco 34) |
| S2. Raft + logs mínimos (1 shard, depois k shards) | `etcd-io/raft` + `wal` do etcd + log segmentado (ADR-055) + gRPC fazem commit com learners em quanto tempo? Quanto tempo leva o catch-up lendo do log segmentado em degradação simulada? | P99 de commit dentro da meta com k fsyncs por shard em paralelo; recuperação correta com queda de host simulada (descartar o que veio depois do último sync do log de replicação); `DurableIndex` nunca à frente do fsync |
| S3. Multi-grupo | A camada própria sustenta um número pequeno de grupos Raft (A3) com heartbeats e RSS dentro da meta? | Se não, avaliar Dragonboat (plano B) |
| S4. Gate, lease e degradado | Modelo TLA+ ampliado (gate por termo, lease, promoção, H-DR13 e H-DR14) + simulação com relógio falso e traces do modelo como testes | Sem violação de invariante; contraexemplos viram decisão ou teste |
| S5. Compatibilidade DynamoDB e conformidade | PutItem/Batch/Get com os SDKs reais; o id estável de retry em cada SDK; `BatchWriteItem` aplicado por itens condicionais ([C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md)); a suíte de conformidade DynamoDB real x camada x Cosmos ([ADR-051](../03_adr/ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)); Query e Scan em GSI recusadas e leitura sem `ConsistentRead` servida como forte ([ADR-050](../03_adr/ADR-050-query-em-gsi-recusada.md)); mTLS nos SDKs; **`t_apply` e a vazão do apply por shard com 1, 4, 16 e 64 workers, com e sem chave quente** ([ADR-057](../03_adr/ADR-057-um-unico-modo-strong-consistency.md), [ADR-058](../03_adr/ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)) | Suíte de contrato e de conformidade verdes; `t_apply` medido dentro do orçamento, ou ajuste do banco ou do orçamento |
| S6. Verificação de versões | Go 1.27, `etcd-io/raft`, gRPC, vtprotobuf | Fixar versões e registrar |
| S7. Bibliotecas | `fasthttp` x `net/http` sob 3.000 TPS com os SDKs Java, Go e .NET; peso do `viper`; `zerolog` sem alocação no caminho quente; escolha do parser JSON (H-DR6) | Números e suíte de compatibilidade (X3) verdes |

**Ordem sugerida de entrega:** S1 → S1b → S2 → S5 → S4 → S3 → v1 (um `DataReplication` com 1 shard) → multi-shard → modo degradado → ferramentas `drctl` → testes de caos.

Os spikes e os testes se encaixam nas **[cinco fases](07_fases_de_trabalho.md)**: S4 e o modelo TLA+ ampliado nascem com as fases 1 e 2; S1, S2 e S4 fecham o núcleo mínimo verificável (fase 3); S3 e S5 acompanham os seis caminhos de banco (fase 4); S6, S7, a conformidade, os game days e a medição do RTO fecham a fase 5.

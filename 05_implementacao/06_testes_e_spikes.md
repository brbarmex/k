# Testes de sistema e plano de spikes

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Índice da implementação](README.md)

## 13. Como vamos testar (resumo)

| Tipo | O que | Ferramenta/abordagem |
|---|---|---|
| Simulação determinística | Relógio, rede e disco falsos; semente reproduzível; injeção de partição, atraso, reordenação, perda e escrita incompleta | Harness próprio. Os testes de interação do próprio `etcd-io/raft` são uma boa base. `testing/synctest` ajuda. |
| Verificação formal | TLA+ do gate, do modo degradado e da saída dele | TLC/Apalache |
| Linearizabilidade | Histórico de operações contra um modelo | Porcupine (estilo Jepsen) |
| Falhas no WAL | Queda em cada ponto da escrita, CRC corrompido, sufixo truncado, fsync com erro | *Failpoints*; `dm-flakey`/`dm-log-writes` em teste de integração |
| Fuzz | Decodificação do WAL, do protocolo gRPC e do JSON do DynamoDB | `go test -fuzz` |
| Caos em cluster | Matar pod, particionar AWS↔Azure, atrasar disco, esgotar disco | Chaos Mesh/LitmusChaos; AWS FIS e Azure Chaos Studio |
| Contrato | Cada SDK/versão contra o endpoint local | Suíte de compatibilidade (X3) |
| Desempenho | Carga a 3.000 TPS com tamanhos reais; medir P99.9 por etapa | Teste de carga com os discos e o link **reais** |

---

## 14. Plano: spikes antes de congelar o desenho

| Spike | Pergunta a responder | Critério de saída |
|---|---|---|
| S1. Medições de infraestrutura | RTT AWS↔Azure (P50/P99), fsync P99 em EBS e em Azure Disk, escrita condicional no DynamoDB (P99 com a cauda) e o custo de ReadIndex em lote | Números reais para o orçamento da seção 7, separando `t_parse`, `t_raft`, `t_azure` e `t_apply` ([C24](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md), [R16](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md)) |
| S2. Raft + WAL mínimo (1 shard) | `etcd-io/raft` + WAL W2 + gRPC fazem commit com learners em quanto tempo? | P99 de commit dentro da meta; WAL sobrevive à bateria de queda |
| S3. Multi-grupo | A camada própria sustenta um número pequeno de grupos Raft (A3) com heartbeats e RSS dentro da meta? | Se não, avaliar Dragonboat (plano B) |
| S4. Gate e degradado | Modelo TLA+ + simulação | Sem violação de invariante |
| S5. Compatibilidade DynamoDB | PutItem/Batch/Get com os SDKs reais; o id estável de retry em cada SDK; `BatchWriteItem` aplicado por itens condicionais ([C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md)) | Suíte de contrato verde |
| S6. Verificação de versões | Go 1.27, `etcd-io/raft`, gRPC, vtprotobuf | Fixar versões e registrar |
| S7. Bibliotecas | `fasthttp` x `net/http` sob 3.000 TPS com os SDKs Java, Go e .NET; peso do `viper`; `zerolog` sem alocação no caminho quente; escolha do parser JSON (H-DR6) | Números e suíte de compatibilidade (X3) verdes |

**Ordem sugerida de entrega:** S1 → S2 → S5 → S4 → S3 → v1 (um `DataReplication` com 1 shard) → multi-shard → modo degradado → ferramentas `drctl` → testes de caos.

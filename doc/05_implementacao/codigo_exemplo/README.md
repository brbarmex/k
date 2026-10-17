# Código de exemplo (esboço compilado)

Esboço do `DataReplication` v1 para fixar o desenho: **não é o código final**. Compilado com Go 1.26.0 (`go build`, `go vet`) e com testes de concorrência verdes (`go test -race`). Corrigido na revisão crítica de 13/10/2026 (oito defeitos, ver [03, seção 7.0](../03_exemplos_de_codigo.md#70-o-que-mudou-na-revisão-crítica-de-13102026)). Para o Go 1.27 (versão decidida), reconfirmar no primeiro build.

| Pasta | Conteúdo |
|---|---|
| `internal/domain/` | `Entry`, `Op`, regra de compactação do log do Raft (`RaftCompactLimit`: aplicado local, durável da maioria Azure dentro da janela, F − 1 de uma reconstrução em curso, folga até o menor `match` AWS limitada por `RAFT_RETAIN_MAX`, ADR-063), regra do que pode sair antes do fsync (`CanSendBeforeSync`, ADR-056), `Gate` (monotônico, só adota o termo atual, `WaitFor` sem vazamento), `Lease` do modo degradado (`t_c + D`, líder nova nasce vencida) com `Clock` injetável, `DegradedPolicy`, com testes de concorrência |
| `internal/port/` | Portas de entrada e saída, incluindo `AppliedWaiter` e `LearnerProgress` (termo, índice, identidade) |
| `internal/usecase/` | `WriteService` (espera de líder, ACK depois do gate, do apply local e da conferência do lease), `DegradeService` (política e lease), `CommitPump` (gate por evento), `Applier` (um item por vez até o gate, progresso por item pela porta `ApplyProgress`, `AppliedWaiter`; versão anterior ao ADR-058: o apply paralelo por chave com marca d'água está em `DataReplicationApp/`), com testes |
| `internal/adapter/in/dynamohttp/` | Adaptador HTTP com `fasthttp`; `WriteTimeout` coerente com o prazo; erros JSON por `json.Marshal`; `x-dr-mode`; Query em GSI recusada (ADR-050); buffer cheio vira `ThrottlingException` (ADR-056) |
| `internal/platform/` | Configuração com `DEGRADED_POLICY` obrigatória, `DEGRADED_LEASE` obrigatória só com `accept`, variáveis do antigo modo Eventual recusadas (ADR-057) e validação de prazos; logger `zerolog` |
| `internal/bootstrap/` | Montagem com Fx (único lugar que importa `fx`); `Degraded` ligado ao estado real do shard; teste do grafo |

```bash
cd codigo_exemplo
go build ./...
go vet ./...
go test -race -count=1 ./...
```

O que **não** existe aqui: adaptador do Raft (`etcdraft` com o `wal` do etcd, que implementaria `ConsensusEngine`, gravaria cada `Ready` com `wal.Save` e `MemoryStorage`, usaria `domain.SplitBeforeSync` no modo síncrono e compactaria até `domain.RaftCompactLimit`), progresso do applier em disco (`ApplyProgress`), ligação do `Applier` no `bootstrap`, parser do JSON do DynamoDB, `DynamoStore` e `CosmosStore`. Ver [03_exemplos_de_codigo.md](../03_exemplos_de_codigo.md) e a [ordem de implementação](../05_testes_armadilhas_e_ordem.md#ordem-sugerida-de-implementação).

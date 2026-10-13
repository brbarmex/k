# Código de exemplo (esboço compilado)

Esboço do `DataReplication` v1 para fixar o desenho: **não é o código final**. Compilado com Go 1.26.0 (`go vet ./...`) e com testes verdes (`go test ./...`). Para o Go 1.27 (versão decidida), reconfirmar no primeiro build.

| Pasta | Conteúdo |
|---|---|
| `internal/domain/` | `Entry`, `Op`, `Gate` (monotônico) e o teste do gate |
| `internal/port/` | Portas de entrada e saída, incluindo `AppliedWaiter` |
| `internal/usecase/` | `WriteService` (ACK espera gate e apply local) com teste, e `CommitPump` |
| `internal/adapter/in/dynamohttp/` | Adaptador HTTP com `fasthttp` e prazo configurável |
| `internal/platform/` | Configuração com validação de prazos e logger `zerolog` |
| `internal/bootstrap/` | Montagem com Fx (único lugar que importa `fx`) |

```bash
cd codigo_exemplo
go vet ./...
go test ./...
```

O que **não** existe aqui: log de replicação, adaptador do Raft, applier, parser do JSON do DynamoDB, `DynamoStore` e `CosmosStore`. Ver [03_exemplos_de_codigo.md](../03_exemplos_de_codigo.md) e a [ordem de implementação](../05_testes_armadilhas_e_ordem.md#ordem-sugerida-de-implementação).

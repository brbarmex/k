# Implementação do DataReplication v1

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md)

Como o código é organizado (ports and adapters, *screaming architecture*), com estrutura de pastas, desenho, diagramas, exemplos compilados e a tabela de structs.

| Arquivo | Conteúdo |
|---|---|
| [01_principios_bibliotecas_e_pastas](01_principios_bibliotecas_e_pastas.md) | Princípios, escolha das bibliotecas com os tradeoffs, árvore de pastas, regras de dependência (`depguard`) |
| [02_desenho_e_diagramas](02_desenho_e_diagramas.md) | Desenho da arquitetura, os dois logs, diagramas de processo (Strong e Eventual, ciclo de vida) e de fluxo (ACK, leitura, estados do nó) |
| [03_exemplos_de_codigo](03_exemplos_de_codigo.md) | Domínio, gate com teste, portas, caso de uso, adaptador HTTP, configuração, Fx |
| [04_tabela_de_structs](04_tabela_de_structs.md) | Nome, pacote e o que cada struct faz |
| [05_testes_armadilhas_e_ordem](05_testes_armadilhas_e_ordem.md) | Testes por camada, armadilhas evitadas, ordem de implementação |
| [06_testes_e_spikes](06_testes_e_spikes.md) | Testes de sistema e os spikes S1 a S7 |
| [codigo_exemplo/](codigo_exemplo/README.md) | Esboço compilado em Go, com testes |

Decisões: Go 1.27, Fx (só no `bootstrap`), `fasthttp`, `grpc-go` com `vtprotobuf`, `zerolog`, `viper` (plano B `koanf`), `etcd-io/raft/v3` (plano B Dragonboat). Ver [ADR-031](../03_adr/ADR-031-hexagonal-e-bibliotecas.md).

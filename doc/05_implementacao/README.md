# Implementação do DataReplication v1

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md)

Como o código é organizado (ports and adapters, *screaming architecture*), com estrutura de pastas, desenho, diagramas, exemplos compilados e a tabela de structs.

| Arquivo | Conteúdo |
|---|---|
| [01_principios_bibliotecas_e_pastas](01_principios_bibliotecas_e_pastas.md) | Princípios, escolha das bibliotecas com os tradeoffs, árvore de pastas, regras de dependência (`depguard`) |
| [02_desenho_e_diagramas](02_desenho_e_diagramas.md) | Desenho da arquitetura, o log do Raft e o `CommitPump`, diagramas de processo (escrita strong, modo degradado, ciclo de vida) e de fluxo (ACK, leitura, estados do nó) |
| [03_exemplos_de_codigo](03_exemplos_de_codigo.md) | Domínio, gate com termo, lease, portas, casos de uso (escrever, degradar, `CommitPump`), adaptador HTTP, configuração, Fx, e os oito defeitos corrigidos com seus testes |
| [04_tabela_de_structs](04_tabela_de_structs.md) | Nome, pacote e o que cada struct faz |
| [05_testes_armadilhas_e_ordem](05_testes_armadilhas_e_ordem.md) | Testes por camada (lease, gate por evento, `I-DEL`, conformidade, mTLS, SKIP por item), armadilhas evitadas, ordem de implementação |
| [06_testes_e_spikes](06_testes_e_spikes.md) | Testes de sistema e os spikes S1 a S7 |
| [07_fases_de_trabalho](07_fases_de_trabalho.md) | As cinco fases do plano: decisões e contratos, TLA+ ampliado, núcleo mínimo verificável, seis caminhos de banco, conformidade e prontidão |
| [codigo_exemplo/](codigo_exemplo/README.md) | Esboço compilado em Go, com testes de concorrência (`go test -race`) |

Decisões: Go 1.27, Fx (só no `bootstrap`), `fasthttp`, `grpc-go` com `vtprotobuf`, `zerolog`, `viper` (plano B `koanf`), `etcd-io/raft/v3` (plano B Dragonboat). Ver [ADR-031](../03_adr/ADR-031-hexagonal-e-bibliotecas.md).

Revisão crítica de 13/10/2026: o esboço de código teve os oito defeitos corrigidos ([03, seção 7.0](03_exemplos_de_codigo.md#70-o-que-mudou-na-revisão-crítica-de-13102026)); o trabalho segue as [cinco fases](07_fases_de_trabalho.md); lease, política de degradação, gate por termo e checkpoint por item entram pelos [ADR-044 a ADR-054](../03_adr/INDEX.md).

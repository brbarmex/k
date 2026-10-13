# Revisão crítica de 12/10/2026: achados e tratamento

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

| # | Achado | Tratamento | Onde |
|---|---|---|---|
| 1 | O gate pode recuar na troca de líder | **Decidido (D9):** a líder nova reconstrói o índice da maioria Azure antes de ACKar | [X01](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md), [ADR-029](../03_adr/ADR-029-gate-monotonico-na-troca-de-lider.md) |
| 2 | `BatchWriteItem` parcial: até 25 itens com índices próprios | **Decidido em 13/10 ([ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md)):** o lote vira uma entrada e o applier usa condicionais por item, porque `BatchWriteItem` não aceita condição | [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md) |
| 3 | Modo degradado invisível ao cliente | **Decidido (D10):** header `x-dr-mode: degraded` e métrica; PEND-02 passa a bloqueante | [ADR-027](../03_adr/ADR-027-sinalizacao-do-modo-degradado.md) |
| 4 | Células follower: ReadIndex na líder e espera de apply local | **Aberto.** Medir nos spikes S1 e S5 | [R16](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md) |
| 5 | `_dr_v` exige reescrever o item (parse) e conta nos 400 KB | **Aberto.** Validar o tamanho antes do commit | [R17](../02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md) |
| 6 | RTO (Delta-T) não quantificado | **Aberto.** Medir | [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md) |
| 7 | Dedupe por hash gera falso positivo para escritas legítimas iguais | **Decidido (D11):** id estável do SDK; hash só como último recurso | [ADR-026](../03_adr/ADR-026-dedupe-de-retry-id-estavel.md), [R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) |
| 8 | Caminho de apply da Azure (Cosmos, RU, 429) | **Aberto.** Tratar RU/429 e medir | [R03](../02_cenarios/a_rever/R03-backlog-de-apply-no-cosmos.md) |
| 9 | IAM por célula e autenticação entre as apps e o `DataReplication` | **Decidido em 13/10 ([ADR-043](../03_adr/ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md)):** uma instalação por cliente; identidade pela configuração; NetworkPolicy do namespace | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) |
| 10 | Custo de seis aplicações por escrita lógica | **Aberto.** Estimar | [R20](../02_cenarios/a_rever/R20-custo-6x.md) |
| 11 | Verificação formal | **Decidido (D12):** modelo TLA+ antes do código do gate | [ADR-030](../03_adr/ADR-030-verificacao-formal-tla.md) |
| 12 | Timeout de 30 s do SDK dos clientes | **Decidido (D13):** prazos em cascata, `REQUEST_DEADLINE` de 7,5 s | [ADR-028](../03_adr/ADR-028-timeouts-em-cascata.md), [X05](../02_cenarios/complexos/X05-timeouts-e-idempotencia.md) |
| 13 | **Novo:** a escrita no DynamoDB está dentro do ACK, então a cauda dele entra na latência | **Decidido em 13/10 ([ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)):** o SLO exclui o banco e é publicado como overhead + banco | [C24](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md) |
| 14 | **Novo:** a janela de dedupe (~58 MiB) não estava no orçamento de memória | **Aberto** | [R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) |

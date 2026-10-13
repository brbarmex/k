# Registro de riscos

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md) · [Pendências e riscos](README.md)

Gravidade pela consequência para o dado ou para a disponibilidade; a ordem é a de atenção recomendada.

| # | Risco | Gravidade | Situação | Cenário |
|---|---|---|---|---|
| 1 | Entrada venenosa trava o shard (apply serial não pode pular) | Alta | Validação pré-commit e `SKIP` sempre por pessoa e auditado ([ADR-038](../03_adr/ADR-038-skip-de-entrada-venenosa-sempre-humano.md)) | [C26](../02_cenarios/atualizados/C26-entrada-venenosa.md) |
| 2 | Disco do log esgota em degradação longa e derruba o commit | Alta | Teto de 70% e reconstrução completa; sem S3 na v1 ([ADR-041](../03_adr/ADR-041-disco-do-log-teto-de-70-por-cento-sem-s3-na-v1.md)) | [C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md) |
| 3 | RPO > 0 sem teto e sem aceite formal | Alta (negócio) | **Bloqueante** | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) |
| 4 | Split-brain na promoção com a AWS viva | Alta | Promoção só humana; discussão externa | [R12](../02_cenarios/a_rever/R12-promocao-com-aws-viva.md) |
| 5 | Cosmos lento enche o disco do learner e degrada a AWS | Alta | Achado desta revisão; precisa de alerta e política de saída | [R03](../02_cenarios/a_rever/R03-backlog-de-apply-no-cosmos.md) |
| 6 | Bug sutil no gate/consenso customizado | Alta | Modelo TLA+ antes do código | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) (H8), [X01](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md) |
| 7 | `BatchWriteItem` não aceita condição: o applier não pode usar o lote do DynamoDB | Média | Decidido: lote = uma entrada, condicionais por item ([ADR-035](../03_adr/ADR-035-lote-batchwriteitem-uma-entrada-e-apply-condicional.md)) | [C23](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md) |
| 8 | Fencing do banco que volta e órfãs no DynamoDB | Média | Decidido: boot cercado, revogação e recriação da célula ([ADR-042](../03_adr/ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md)); a extração das órfãs precisa vir antes | [C30](../02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md) |
| 9 | Latência: cauda do DynamoDB no ACK e faixa cinzenta do link | Média | SLO exclui o banco e é publicado como overhead + banco ([ADR-036](../03_adr/ADR-036-slo-de-50ms-exclui-a-escrita-no-banco.md)); faixa cinzenta só alerta ([ADR-037](../03_adr/ADR-037-faixa-cinzenta-do-link-so-alerta.md)); medir no spike S1 | [C24](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md), [C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) |
| 10 | Parse completo do JSON compete com heartbeat e fsync por CPU | Média | Aceito pelo Bruno; medir no S2 | [ADR-032](../03_adr/ADR-032-parse-json-completo-e-rust.md) |
| 11 | Pod único por célula: reinício derruba o endpoint da célula | Média | Partida rápida, PDB, transferência de liderança | [ADR-016](../03_adr/ADR-016-app-unico-por-celula-monolito-modular.md) |
| 12 | Custo: seis gravações por escrita lógica | Média | Estimar | [R20](../02_cenarios/a_rever/R20-custo-6x.md) |
| 13 | Memória da janela de dedupe e ABA fora da janela | Média | Achado desta revisão | [R22](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) |
| 14 | Dependência circular da promoção em recursos da AWS | Média | Teste com AWS bloqueada | [R11](../02_cenarios/a_rever/R11-api-de-promocao-depende-da-aws.md) |
| 15 | RTO sem número | Média | Medir | [R18](../02_cenarios/a_rever/R18-rto-nao-quantificado.md) |
| 16 | Cabo dedicado compartilhado entre as instalações dos clientes: um cliente satura a banda dos outros | Alta | Decidido 13a: banda reservada ou limitada por cliente; falta implementar e medir | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md), [C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) |
| 17 | Faixas de IP sobrepostas entre contas de clientes quebram o tráfego transitivo pelo cabo | Média | Premissa de plano central (14a); confirmar com a rede | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) |
| 18 | Operar N instalações isoladas (versões, certificados, plantão de promoção) | Média | Novo (13/10); processo de frota a definir | [C31](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) |

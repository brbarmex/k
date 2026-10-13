# ADR-054 — Um banco por célula mantido, com o trade-off registrado

**Status:** Decidido (Bruno, 13/10/2026): manter seis bancos · **Data:** 13/10/2026

## Contexto

A revisão crítica lembrou que o DynamoDB é regional e já replica por três AZs: três tabelas na mesma região **não são três domínios de falha físicos**, e o mesmo vale para o Cosmos com redundância de zona. A alternativa seria um banco regional por nuvem (duas materializações), com a líder como única aplicadora.

## Decisão

**Manter um banco por célula** ([ADR-022](ADR-022-banco-por-celula-nome-logico.md)), como o Bruno decidiu. O que as seis cópias compram é **isolamento lógico**: corrupção ou throttling de uma tabela não atinge as outras, e o applier de cada célula não depende das demais. A durabilidade por AZ vem dos três logs Raft, não dos bancos.

## Alternativas consideradas

- **Um banco regional por nuvem:** menos custo, menos fluxos de reconciliação, fencing e rebuild mais simples. Riscos: um ponto lógico de corrupção na AWS, throttling compartilhado entre escrita ao vivo e replay.
- **ADR de trade-off com estimativa de custo antes de decidir:** substituída pela decisão direta.

## Consequências

- Seis materializações por escrita lógica, seis fluxos de reconciliação, fencing e rebuild por banco: custo (R20), throttling e protocolos do [ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md) valem por célula.
- A estimativa de custo por cliente continua necessária ([R20](../02_cenarios/a_rever/R20-custo-6x.md)): a decisão é de arquitetura, o custo é um número a conhecer.
- O motivo da decisão (padrão de bulkhead da organização ou escolha do projeto) não foi registrado; se for regra da organização, vale citar a fonte aqui.

## Onde está o detalhe

- [05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md](../01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md)
- [R20-custo-6x.md](../02_cenarios/a_rever/R20-custo-6x.md)

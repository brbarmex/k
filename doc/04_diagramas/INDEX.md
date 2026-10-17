# Catálogo de diagramas

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md)

Todos os 86 diagramas **Mermaid** do pacote, por pasta. Os diagramas de cada cenário ficam junto do cenário; os de arquitetura estão em `D01` a `D08` (este diretório).

## Diagramas de arquitetura (este diretório)

| Arquivo | Assunto |
|---|---|
| [D01](D01-visao-do-sistema.md) | Contexto (mTLS por aplicação) e regra do ACK (gate por termo, `accept` com lease, `reject` com `503 PENDING`) |
| [D02](D02-implantacao-da-celula.md) | Pod, namespace, volume, IAM e lista de permissões por aplicação |
| [D03](D03-camadas-hexagonais.md) | Regra de dependência entre camadas |
| [D04](D04-commitpump-e-log-do-raft.md) | Caminho do dado, o log do Raft, group commit por shard (nada sai antes do fsync), gate por evento e progresso do applier por item |
| [D05](D05-gate-e-quorum.md) | Composição do gate: termo atual, evento, lease e barreira da leitura forte |
| [D06](D06-estados-do-shard-e-do-no.md) | Modos do shard (lease expirado, sem gate com `reject`, promoção depois de D + M) e estados do nó |
| [D07](D07-prazos-em-cascata.md) | Prazos dentro dos 30 s do SDK e o lease D fora da cascata |
| [D08](D08-uma-escrita-seis-bancos.md) | Fan-out da escrita (seis bancos mantidos, ADR-054) e onde está o ACK |

> Os diagramas D03, D04 e D06 têm versão ilustrativa em [05_implementacao/02_desenho_e_diagramas](../05_implementacao/02_desenho_e_diagramas.md); ao mudar um, revisar o outro.

## Diagramas de arquitetura

| Arquivo | Seção | Tipo |
|---|---|---|
| [D01-visao-do-sistema.md](../04_diagramas/D01-visao-do-sistema.md) | Contexto | fluxo |
| [D01-visao-do-sistema.md](../04_diagramas/D01-visao-do-sistema.md) | Regra do ACK | fluxo |
| [D02-implantacao-da-celula.md](../04_diagramas/D02-implantacao-da-celula.md) | Dentro de uma célula | fluxo |
| [D03-camadas-hexagonais.md](../04_diagramas/D03-camadas-hexagonais.md) | Regra de dependência | fluxo |
| [D04-commitpump-e-log-do-raft.md](../04_diagramas/D04-commitpump-e-log-do-raft.md) | Caminho do dado em um nó | fluxo |
| [D05-gate-e-quorum.md](../04_diagramas/D05-gate-e-quorum.md) | Composição do gate | fluxo |
| [D06-estados-do-shard-e-do-no.md](../04_diagramas/D06-estados-do-shard-e-do-no.md) | Estados do nó | estados |
| [D06-estados-do-shard-e-do-no.md](../04_diagramas/D06-estados-do-shard-e-do-no.md) | Modos de operação do shard | estados |
| [D07-prazos-em-cascata.md](../04_diagramas/D07-prazos-em-cascata.md) | Cascata de prazos | fluxo |
| [D08-uma-escrita-seis-bancos.md](../04_diagramas/D08-uma-escrita-seis-bancos.md) | Fan-out da escrita | fluxo |

## Índice de cenários

| Arquivo | Seção | Tipo |
|---|---|---|
| [INDEX.md](../02_cenarios/INDEX.md) | Como os cenários se conectam | fluxo |

## Cenários atualizados

| Arquivo | Seção | Tipo |
|---|---|---|
| [C01-visao-geral.md](../02_cenarios/atualizados/C01-visao-geral.md) | Diagrama | fluxo |
| [C02-dentro-da-celula.md](../02_cenarios/atualizados/C02-dentro-da-celula.md) | Diagrama | fluxo |
| [C03-escrita-strong.md](../02_cenarios/atualizados/C03-escrita-strong.md) | Diagrama | sequência |
| [C05-leitura-strong.md](../02_cenarios/atualizados/C05-leitura-strong.md) | Diagrama | sequência |
| [C06-follower-encaminha.md](../02_cenarios/atualizados/C06-follower-encaminha.md) | Diagrama | sequência |
| [C07-timeout-depois-do-commit.md](../02_cenarios/atualizados/C07-timeout-depois-do-commit.md) | Diagrama | sequência |
| [C07-timeout-depois-do-commit.md](../02_cenarios/atualizados/C07-timeout-depois-do-commit.md) | Diagrama: prazos em cascata | fluxo |
| [C08-queda-de-celula-lider-ou-az-aws.md](../02_cenarios/atualizados/C08-queda-de-celula-lider-ou-az-aws.md) | Diagrama | sequência |
| [C09-queda-de-az-azure.md](../02_cenarios/atualizados/C09-queda-de-az-azure.md) | Diagrama | sequência |
| [C10-celulas-de-app-aws-fora.md](../02_cenarios/atualizados/C10-celulas-de-app-aws-fora.md) | Diagrama | sequência |
| [C11-transferencia-planejada.md](../02_cenarios/atualizados/C11-transferencia-planejada.md) | Diagrama | sequência |
| [C12-modo-degradado.md](../02_cenarios/atualizados/C12-modo-degradado.md) | Diagrama | fluxo |
| [C13-ressincronizacao-da-azure.md](../02_cenarios/atualizados/C13-ressincronizacao-da-azure.md) | Diagrama | sequência |
| [C14-aws-cai-sem-degradacao.md](../02_cenarios/atualizados/C14-aws-cai-sem-degradacao.md) | Diagrama | sequência |
| [C15-aws-cai-em-degradacao.md](../02_cenarios/atualizados/C15-aws-cai-em-degradacao.md) | Diagrama | sequência |
| [C16-aws-volta-apos-promocao.md](../02_cenarios/atualizados/C16-aws-volta-apos-promocao.md) | Diagrama | sequência |
| [C17-maquina-de-estados.md](../02_cenarios/atualizados/C17-maquina-de-estados.md) | Diagrama | estados |
| [C18-partida-e-recuperacao-do-no.md](../02_cenarios/atualizados/C18-partida-e-recuperacao-do-no.md) | Diagrama: estados do nó | estados |
| [C18-partida-e-recuperacao-do-no.md](../02_cenarios/atualizados/C18-partida-e-recuperacao-do-no.md) | Diagrama: partida e parada | fluxo |
| [C19-falha-do-datareplication.md](../02_cenarios/atualizados/C19-falha-do-datareplication.md) | Diagrama | fluxo |
| [C20-bypass-de-escrita-direta.md](../02_cenarios/atualizados/C20-bypass-de-escrita-direta.md) | Diagrama | fluxo |
| [C21-disco-fsync-e-reconstrucao.md](../02_cenarios/atualizados/C21-disco-fsync-e-reconstrucao.md) | Diagrama | fluxo |
| [C22-cliente-com-sdk-nao-suportado.md](../02_cenarios/atualizados/C22-cliente-com-sdk-nao-suportado.md) | Diagrama | fluxo |
| [C23-batchwriteitem-lote-uma-entrada.md](../02_cenarios/atualizados/C23-batchwriteitem-lote-uma-entrada.md) | Diagrama: Lote como uma entrada, apply por item | fluxo |
| [C24-orcamento-de-50ms-e-escrita-no-dynamodb.md](../02_cenarios/atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md) | Diagrama: O que entra no caminho do ACK | fluxo |
| [C25-link-lento-faixa-cinzenta.md](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md) | Diagrama: Três faixas de latência do gate | fluxo |
| [C26-entrada-venenosa.md](../02_cenarios/atualizados/C26-entrada-venenosa.md) | Diagrama: Defesa em camadas | fluxo |
| [C27-encaminhamento-sem-lider-conhecida.md](../02_cenarios/atualizados/C27-encaminhamento-sem-lider-conhecida.md) | Diagrama: Salto único e termo no encaminhamento | fluxo |
| [C29-disco-do-log-em-degradacao-longa.md](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md) | Diagrama: Teto de retenção | fluxo |
| [C30-aws-que-volta-boot-cercado.md](../02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md) | Diagrama: Boot cercado | fluxo |
| [C31-implantacao-por-cliente-e-identidade.md](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md) | Diagrama: Fronteiras de confiança e o que é compartilhado | fluxo |
| [C32-particao-e-lease-do-modo-degradado.md](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) | Diagrama: linha do tempo da partição | sequência |
| [C32-particao-e-lease-do-modo-degradado.md](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md) | Diagrama: o que cada ponta decide | fluxo |
| [C33-reconstrucao-em-tabela-sombra.md](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md) | Diagrama: protocolo da reconstrução | sequência |
| [C33-reconstrucao-em-tabela-sombra.md](../02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md) | Diagrama: por que a cópia difusa converge | fluxo |
| [C34-autorizacao-por-aplicacao.md](../02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md) | Diagrama: decisão de autorização na entrada | fluxo |
| [C34-autorizacao-por-aplicacao.md](../02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md) | Diagrama: privilégio hoje, sem lista e com a lista | fluxo |

## Cenários complexos

| Arquivo | Seção | Tipo |
|---|---|---|
| [X01-gate-e-troca-de-lider.md](../02_cenarios/complexos/X01-gate-e-troca-de-lider.md) | Diagrama: troca de líder | fluxo |
| [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md) | Diagrama: entrada e saída | estados |
| [X03-desastre-e-promocao-da-azure.md](../02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md) | Diagrama: promoção | fluxo |
| [X04-partida-e-recuperacao-do-no.md](../02_cenarios/complexos/X04-partida-e-recuperacao-do-no.md) | Diagrama: janelas de queda | sequência |
| [X05-timeouts-e-idempotencia.md](../02_cenarios/complexos/X05-timeouts-e-idempotencia.md) | Diagrama | fluxo |
| [X06-delete-ttl-gsi.md](../02_cenarios/complexos/X06-delete-ttl-gsi.md) | Diagrama: zumbi e a ordem por chave | sequência |

## Cenários a rever (ainda sem decisão)

| Arquivo | Seção | Tipo |
|---|---|---|
| [R03-backlog-de-apply-no-cosmos.md](../02_cenarios/a_rever/R03-backlog-de-apply-no-cosmos.md) | Diagrama: Do 429 ao RPO | fluxo |
| [R06-duas-azs-aws-caem.md](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md) | Diagrama: Quem pode continuar | fluxo |
| [R07-link-oscilando.md](../02_cenarios/a_rever/R07-link-oscilando.md) | Diagrama: Histerese e episódio | fluxo |
| [R09-resincronizacao-lenta-ou-bloqueada.md](../02_cenarios/a_rever/R09-resincronizacao-lenta-ou-bloqueada.md) | Diagrama: Convergência do catch-up | fluxo |
| [R10-operador-lento-no-desastre.md](../02_cenarios/a_rever/R10-operador-lento-no-desastre.md) | Diagrama: Linha do tempo do RTO com promoção | fluxo |
| [R11-api-de-promocao-depende-da-aws.md](../02_cenarios/a_rever/R11-api-de-promocao-depende-da-aws.md) | Diagrama: Teste com a AWS bloqueada | fluxo |
| [R12-promocao-com-aws-viva.md](../02_cenarios/a_rever/R12-promocao-com-aws-viva.md) | Diagrama: Defesa em profundidade na promoção | fluxo |
| [R13-escritas-orfas-reconciliacao.md](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md) | Diagrama: Do episódio degradado ao relatório | fluxo |
| [R16-leitura-forte-em-celulas-follower.md](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md) | Diagrama: Caminho da leitura forte | fluxo |
| [R17-atributo-de-versao-e-limite-400kb.md](../02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md) | Diagrama: Validação de tamanho antes do commit | fluxo |
| [R17-atributo-de-versao-e-limite-400kb.md](../02_cenarios/a_rever/R17-atributo-de-versao-e-limite-400kb.md) | Diagrama: O atributo não sai nas respostas | fluxo |
| [R18-rto-nao-quantificado.md](../02_cenarios/a_rever/R18-rto-nao-quantificado.md) | Diagrama: De onde vem o RTO | fluxo |
| [R20-custo-6x.md](../02_cenarios/a_rever/R20-custo-6x.md) | Diagrama: Onde o custo se multiplica | fluxo |
| [R22-memoria-da-janela-de-dedupe.md](../02_cenarios/a_rever/R22-memoria-da-janela-de-dedupe.md) | Diagrama: Tabela mantida por todos os votantes | fluxo |
| [R23-hipoteses-gerais.md](../02_cenarios/a_rever/R23-hipoteses-gerais.md) | H4 / PEND-11. Líder lenta, mas viva | fluxo |

## Implementação

| Arquivo | Seção | Tipo |
|---|---|---|
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 4. Desenho da arquitetura | fluxo |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 4.2 Como o Raft e a replicação atuam | fluxo |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 4.2 Como o Raft e a replicação atuam | fluxo |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 5.1 Escrita em modo Strong (caminho feliz) | sequência |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 5.1b Escrita em modo degradado (política `accept`, com lease) | sequência |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 5.2 Ciclo de vida do processo (partida e parada) | fluxo |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 6.1 Decisão de ACK (gate, política e lease) | fluxo |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 6.2 Leitura (sempre forte, ADR-057) | fluxo |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 6.3 Estados do nó | estados |
| [02_desenho_e_diagramas.md](../05_implementacao/02_desenho_e_diagramas.md) | 6.4 Estados do modo degradado (lease) | estados |
| [03_exemplos_de_codigo.md](../05_implementacao/03_exemplos_de_codigo.md) | 7.6b Prazos em cascata (timeouts) | fluxo |
| [07_fases_de_trabalho.md](../05_implementacao/07_fases_de_trabalho.md) | Visão geral das cinco fases | fluxo |

## Arquitetura

| Arquivo | Seção | Tipo |
|---|---|---|
| [01_forma_do_app_e_escopo.md](../01_arquitetura/01_forma_do_app_e_escopo.md) | 3.1 Modelo de implantação | fluxo |

## Documento principal

| Arquivo | Seção | Tipo |
|---|---|---|
| [DataReplication_Versao_1.md](../DataReplication_Versao_1.md) | 2. A solução em uma página | fluxo |
| [DataReplication_Versao_1.md](../DataReplication_Versao_1.md) | 6.1 Escrita (modo Strong) | fluxo |
| [DataReplication_Versao_1.md](../DataReplication_Versao_1.md) | 6.2 Estados do shard | estados |

## Pendências e análises

| Arquivo | Seção | Tipo |
|---|---|---|
| [analise_group_commit_e_async_storage_13_10.md](../06_pendencias_e_riscos/analise_group_commit_e_async_storage_13_10.md) | 3. `AsyncStorageWrites` (fluxo da escrita com respostas presas ao fsync) | sequência |

# Índice dos cenários

Todo cenário tem um diagrama Mermaid, as etapas e as falhas com a tratativa. Os arquivos estão em três pastas, de acordo com a situação.

| Pasta | Significado | Quantidade |
|---|---|---|
| [`atualizados/`](atualizados/) | Cenário já decidido e redesenhado no **modelo v1** (learners, gate, dois logs, prazos) | 34 |
| [`complexos/`](complexos/) | Assuntos que precisam de mais detalhe; cada um é referenciado pelos cenários que dependem dele | 6 |
| [`a_rever/`](a_rever/) | Cenários e hipóteses **ainda sem decisão completa**, com a proposta de fluxo e as perguntas em aberto | 14 |

**Legenda de status.** ✅ atualizado · ⚠️ proposta ainda não aprovada · 🟡 parcialmente tratado · ❓ sem resolução · 🆕 achado novo desta revisão.

## Atualizados (modelo v1)

| Id | Cenário | Ponto-chave |
|---|---|---|
| [C01](atualizados/C01-visao-geral.md) | Visão geral da arquitetura | Mapa das peças: clouds, células, link, roteamento externo |
| [C02](atualizados/C02-dentro-da-celula.md) | Dentro da célula: apps do cliente e DataReplication | Um `DataReplication` por célula, sem sidecar |
| [C03](atualizados/C03-escrita-strong.md) | Caminho feliz: escrita (apply serial depois do gate) | Raft + gate por evento no termo atual; apply sempre serial depois do gate; ACK depois do apply na célula da líder |
| [C05](atualizados/C05-leitura-strong.md) | Caminho feliz: leitura strong | Espera `min(ReadIndex, gate)`; Query em GSI dá 400 (ADR-049, ADR-050, ADR-057) |
| [C06](atualizados/C06-follower-encaminha.md) | Requisição chega numa célula que não é a líder | Follower encaminha à líder, um salto |
| [C07](atualizados/C07-timeout-depois-do-commit.md) | Prazo estoura depois do commit, antes do ACK (retry do SDK) | `503 PENDING`, dedupe e versão condicional |
| [C08](atualizados/C08-queda-de-celula-lider-ou-az-aws.md) | Queda da célula líder ou de uma AZ na AWS | Eleição e reconstrução do gate |
| [C09](atualizados/C09-queda-de-az-azure.md) | Queda de uma AZ na Azure | Gate com 2 de 3; sem maioria vira C12 |
| [C10](atualizados/C10-celulas-de-app-aws-fora.md) | Apps da AWS fora, replicação viva: tráfego vai para a Azure | Escrita cruza o link duas vezes |
| [C11](atualizados/C11-transferencia-planejada.md) | Transferência planejada de liderança (manutenção) | Barreira e pausa curta, sem perda |
| [C12](atualizados/C12-modo-degradado.md) | Link AWS↔Azure cai ou Azure cai: modo degradado | `DEGRADE` com lease e `DEGRADED_POLICY`; header `x-dr-mode` (ADR-044, ADR-045) |
| [C13](atualizados/C13-ressincronizacao-da-azure.md) | Ressincronização da Azure | Catch-up do log; `RESTORE` com barreira |
| [C14](atualizados/C14-aws-cai-sem-degradacao.md) | AWS cai por inteiro sem degradação prévia | Promoção humana, RPO 0 |
| [C15](atualizados/C15-aws-cai-em-degradacao.md) | AWS cai durante o modo degradado | Promoção humana, perda declarada (órfãs) |
| [C16](atualizados/C16-aws-volta-apos-promocao.md) | AWS volta depois da promoção da Azure | Learner com identidade nova; failback planejado |
| [C17](atualizados/C17-maquina-de-estados.md) | Máquina de estados dos modos de operação de um shard | Normal, degradado, ressincronizando, promovido |
| [C18](atualizados/C18-partida-e-recuperacao-do-no.md) | Partida, recuperação e parada limpa do nó | Ordem de partida e parada limpa |
| [C19](atualizados/C19-falha-do-datareplication.md) | O DataReplication da célula cai ou reinicia | Pod reinicia: recuperação dos dois logs |
| [C20](atualizados/C20-bypass-de-escrita-direta.md) | Aplicação escreve direto no banco (bypass) | Controle por IAM e mTLS por aplicação; detecção por auditoria (ADR-052) |
| [C21](atualizados/C21-disco-fsync-e-reconstrucao.md) | Falha de fsync, corrupção, disco lento e disco perdido | Fsync fatal, identidade nova, `drctl rebuild` |
| [C22](atualizados/C22-cliente-com-sdk-nao-suportado.md) | Cliente com SDK ou versão não suportada | Rejeição por User-Agent depois do período de alerta |
| [C23](atualizados/C23-batchwriteitem-lote-uma-entrada.md) | BatchWriteItem: o lote é uma entrada e o apply é por item | Uma entrada; `PutItem`/`DeleteItem` condicionais por item (ADR-035) |
| [C24](atualizados/C24-orcamento-de-50ms-e-escrita-no-dynamodb.md) | Orçamento de 50 ms e a escrita no DynamoDB dentro do ACK | SLO exclui o banco; publicado como overhead + banco (ADR-036) |
| [C25](atualizados/C25-link-lento-faixa-cinzenta.md) | Link lento ou saturado (faixa cinzenta) | Só alerta; RPO continua 0 (ADR-037) |
| [C26](atualizados/C26-entrada-venenosa.md) | Entrada venenosa: um item que o banco recusa para sempre | Validação pré-commit; `SKIP` por item `(índice, ordem)`, sempre por pessoa (ADR-038, ADR-048) |
| [C27](atualizados/C27-encaminhamento-sem-lider-conhecida.md) | Loop de encaminhamento e espera pela líder durante a eleição | Um salto, termo no encaminhamento; espera até 1 s e `503` (ADR-039) |
| [C29](atualizados/C29-disco-do-log-em-degradacao-longa.md) | Disco do log esgota durante uma degradação longa | Teto de 70% e reconstrução completa; sem S3 (ADR-041) |
| [C30](atualizados/C30-aws-que-volta-boot-cercado.md) | Fencing: o DynamoDB e o applier da AWS que voltam depois da promoção | Boot cercado, credenciais revogadas, célula recriada (ADR-042) |
| [C31](atualizados/C31-implantacao-por-cliente-e-identidade.md) | Uma instalação por cliente e identificação na API | 1 para 1; cliente pela configuração (ADR-043) |
| [C32](atualizados/C32-particao-e-lease-do-modo-degradado.md) | Partição entre AWS e Azure: lease do modo degradado e promoção da Azure | `DEGRADE` com validade D, `503 LEASE_EXPIRED`, `ForcePromote` só depois de D + M, RTO >= D + M (ADR-044, ADR-045) |
| [C33](atualizados/C33-reconstrucao-em-tabela-sombra.md) | Reconstrução do banco de uma célula em tabela sombra | Índice F, cópia difusa, replay, cutover com geração do nome lógico, checksum por faixa (ADR-047) |
| [C34](atualizados/C34-autorizacao-por-aplicacao.md) | Autorização por aplicação: mTLS e lista de permissões por identidade | mTLS por aplicação, lista identidade → tabelas e operações, `403`; "só trocar o endpoint" deixa de valer (ADR-052) |

## Complexos (detalhamento)

| Id | Assunto | Conteúdo |
|---|---|---|
| [X01](complexos/X01-gate-e-troca-de-lider.md) | Gate: definição, monotonicidade e troca de líder | Propriedades P1 a P18 do modelo TLA+ (P9 a P18 vêm da revisão de 13/10) |
| [X02](complexos/X02-modo-degradado.md) | Modo degradado: entrada, permanência e saída | Entrada, lease D, `DEGRADED_POLICY`, saída e limiares |
| [X03](complexos/X03-desastre-e-promocao-da-azure.md) | Desastre: promoção da Azure, fencing e reconciliação | Epoch, promoção só depois de D + M, escolha do log, órfãs, failback |
| [X04](complexos/X04-partida-recuperacao-e-dois-logs.md) | Partida, recuperação e coerência entre os dois logs | CommitPump e truncamento |
| [X05](complexos/X05-timeouts-e-idempotencia.md) | Timeouts em cascata e idempotência do retry | Prazos em cascata e janela de dedupe |
| [X06](complexos/X06-delete-ttl-gsi.md) | DELETE, TTL, Scan e GSI | Delete real com invariante `I-DEL`, TTL local, Query em GSI recusada |
| [X07](complexos/X07-gate-explicado-com-exemplos.md) | O gate explicado com exemplos | Regra, quando atua e 6 cenários com índices (feliz, Azure atrasada, uma Azure fora, link caído, troca de líder, POC só AWS) |
| [X08](complexos/X08-leitura-forte-com-exemplos.md) | Leitura forte com dados mock | Barreira min(ReadIndex, gate) passo a passo, quando a leitura trava (T1 a T7) e o cenário oculto da líder nova com a Azure fora |

## A rever (em aberto)

| Id | Cenário | Situação e o que falta |
|---|---|---|
| [R03](a_rever/R03-backlog-de-apply-no-cosmos.md) | Backlog de apply no Cosmos (429 e RU insuficiente) | 🟡 parcialmente tratado: Cosmos lento pode encher disco do learner e degradar a AWS; limite de lag de apply para elegibilidade ao failover é a PEND-18 |
| [R06](a_rever/R06-duas-azs-aws-caem.md) | Duas AZs da AWS caem com a Azure saudável | 🟡 desenho decidido, falta D e a prova no TLA+ (lease, ADR-044); "degradar só com 3 de 3" fica como alternativa a comparar no modelo |
| [R07](a_rever/R07-link-oscilando.md) | Link oscilando (flapping) | ⚠️ proposta ainda não aprovada: Histerese, episódio com `fromIndex` estável |
| [R09](a_rever/R09-resincronizacao-lenta-ou-bloqueada.md) | Ressincronização da Azure lenta ou que nunca termina | 🟡 parcialmente tratado: ETA do catch-up; QoS; reconstrução |
| [R10](a_rever/R10-operador-lento-no-desastre.md) | Tempo de decisão humana no desastre | ❓ sem resolução: `drctl promote`, plantão, game days |
| [R11](a_rever/R11-api-de-promocao-depende-da-aws.md) | A API de promoção depende de algo que estava na AWS | 🟡 parcialmente tratado: Checklist de dependências; teste com AWS bloqueada |
| [R12](a_rever/R12-promocao-com-aws-viva.md) | Promoção da Azure com a AWS viva (split-brain, PEND-01) | 🟡 desenho decidido, falta D e a prova no TLA+ (ADR-044) |
| [R13](a_rever/R13-escritas-orfas-reconciliacao.md) | Escritas órfãs: reconciliação depois da promoção (PEND-03) | 🟡 janela das órfãs limitada por D; extração antes da recriação do banco (ADR-042, C33); falta dono e política com o cliente (PEND-03) |
| [R16](a_rever/R16-leitura-forte-em-celulas-follower.md) | Leitura forte em células follower (ReadIndex) | 🟡 resolvido em parte pelo ADR-049 (`min(ReadIndex, gate)`); falta medir leituras fortes e definir o SLO de leitura |
| [R17](a_rever/R17-atributo-de-versao-e-limite-400kb.md) | Atributo de versão e o limite de 400 KB do item | 🟡 regra "`_dr_v` fora das respostas" decidida (ADR-051); falta nome (H-DR3), reserva e levantamento de itens grandes |
| [R18](a_rever/R18-rto-nao-quantificado.md) | RTO (Delta-T) ainda sem número | ❓ método decidido (distribuição medida em exercícios de falha e SLO ponta a ponta); falta medir |
| [R20](a_rever/R20-custo-6x.md) | Custo: cada escrita lógica vira seis gravações | 🟡 seis bancos mantidos (ADR-054); falta estimativa por cliente e teto |
| [R22](a_rever/R22-memoria-da-janela-de-dedupe.md) | Memória da janela de dedupe | 🟡 limite da idempotência documentado (ADR-051); falta medir memória e confirmar o id estável por SDK |
| [R23](a_rever/R23-hipoteses-gerais.md) | Hipóteses gerais em aberto | ❓ sem resolução: H2/PEND-02 (bloqueante), H4, H7 a H11, H-DR1/4/6 |

## Como os cenários se conectam

```mermaid
flowchart LR
    C["C01 a C34: atualizados"] -->|"detalham em"| X["X01 a X08: complexos"]
    C -->|"pontos em aberto apontam para"| R["R03 a R23: a rever"]
    X -->|"pontos em aberto apontam para"| R
    R -->|"quando decidido, vira"| A["ADR novo e cenário atualizado"]
```

Quando um item de `a_rever/` é decidido, ele passa para `atualizados/` (ou `complexos/`), ganha o ADR correspondente e a linha daqui muda.

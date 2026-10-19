# Validação da POC na AWS (EKS, gp3): cenários de teste

Objetivo: provar, com observabilidade, que a POC do DataReplication atende **RPO = 0**, **tolerância a falhas** e **alta disponibilidade** rodando na AWS (3 células em 3 AZs, EKS, discos gp3, DynamoDB por célula), e **medir o RTO (Delta-T)**, que ainda não tem número.

Cada arquivo é um cenário. Todos seguem o método comum em [00_metodo_e_observabilidade.md](00_metodo_e_observabilidade.md): ambiente (AMB), cargas (CRG), oráculo do RPO (ORC), medição do RTO (RTO), catálogo de métricas com p90, p99, p99.9 e max (L), golden signals (G), métricas além deles (B) e lacunas de instrumentação (M).

Base: código local de `DataReplicationApp/` no commit `c9f42af` (18/10/2026), **revista em 18/10 para o commit `eec02c9`**, que liga a compactação do log do Raft (Incremento A da [trecho da compactação](../spec/compactacao_do_log/spec_poc_trecho.md)): snapshot-marcador, RAM e disco limitados, partida pela cauda e `MsgSnap` sem queda. A recuperação de uma célula atrás da cauda e a reconstrução são do Incremento B. É especificação: nada aqui altera a POC.

## Três avisos antes de executar

1. **Observabilidade do app não prova RPO = 0 sozinha.** `commit = gate = aplicado` nas 3 células diz que o app acha que está tudo certo. A prova é o **oráculo**: o ledger do gerador de carga confrontado com o que está nas 3 tabelas, publicado como métrica (ORC-05). Sem o oráculo, nenhum cenário de RPO pode ser marcado como comprovado.
2. **p99.9 e max precisam de fonte certa.** Histograma clássico do Prometheus não tem max, e os baldes atuais (fator ~2) erram o p99.9 por quase 2×. A fonte da verdade é o cliente (k6 com `p(90)`, `p(99)`, `p(99.9)`, `max`); o servidor explica onde o tempo foi (M-03).
3. **Não há manifestos de EKS no repositório.** Os cenários assumem o ambiente descrito em AMB-01 a AMB-12, que precisa ser especificado à parte.

## Análise da lista original

| Item da lista | Avaliação | Arquivo |
|---|---|---|
| Replicação com tráfego na líder | Bem definido. Faltava o critério objetivo (oráculo) e separar o tempo do DynamoDB do tempo do Raft no ACK. Vira o **baseline** de todos. | [CF-01](CF-01-replicacao-trafego-na-lider.md) |
| Replicação com tráfego na follower | Bem definido. Faltava a métrica do encaminhamento (não existe) e o risco do log por escrita encaminhada. | [CF-02](CF-02-replicacao-trafego-na-follower.md) |
| Troca de liderança abrupta por comando | **Mal definido e sem suporte.** Não há comando de troca na POC (saiu em 17/10) e "comando" e "abrupto" são opostos. Separado em 4 variantes; as que rodam hoje são SIGTERM e SIGKILL da líder. | [CE-01](CE-01-troca-de-lideranca-abrupta-com-trafego.md) |
| [pod-restart] 1 follower | Faltava dizer **como** reinicia (SIGTERM, SIGKILL, reinício do container): são três caminhos. | [FP-01](FP-01-pod-restart-uma-follower.md) |
| [pod-restart] todas as followers | É **perda de quórum**: valida RPO, não HA. Ponto oculto: escritas PENDING podem ser confirmadas minutos depois. | [FP-02](FP-02-pod-restart-todas-as-followers.md) |
| [pod-restart] líder | É o que mede o Delta-T principal; precisa da decomposição do RTO e de 10 repetições. | [FP-03](FP-03-pod-restart-lider.md) |
| [pod-outage-10min] 1 follower | O ponto não é a queda, é a **volta**: a vazão do apply pode ser menor que a entrada e a célula nunca alcançar. | [FP-04](FP-04-pod-outage-10min-uma-follower.md) |
| [pod-outage-10min] todas as followers | Igual a FP-02, prolongado. É dado para a apresentação, não falha da POC. | [FP-05](FP-05-pod-outage-10min-todas-as-followers.md) |
| [pod-outage-10min] líder | Junta FP-03, a cauda descartada da antiga e o atraso de apply da nova líder. | [FP-06](FP-06-pod-outage-10min-lider.md) |
| [disco] na follower | **"Comando ou desanexar" são falhas opostas**: pausa de I/O trava sem erro; desanexar dá erro e derruba. Separadas em variantes. A volta com o mesmo NODE_ID viola o ADR-034. | [FD-01](FD-01-problema-de-disco-na-follower.md) |
| [disco] na líder | A pausa de I/O deixa a líder antiga **pronta para o Kubernetes e sem escrita** durante toda a pausa. | [FD-02](FD-02-problema-de-disco-na-lider.md) |
| Caminho replicação: caminho feliz | Preenchido: o que é "replicado" no instante do 200 e o sinal de cada passo. | [RP-01](RP-01-caminho-de-replicacao-feliz.md) |
| Caminho replicação: caminho de falha | Preenchido: 14 pontos de falha, resposta ao cliente e destino do dado. | [RP-02](RP-02-caminho-de-replicacao-com-falha.md) |

## Índice completo e suporte na POC hoje

Legenda: **Sim** roda e deve passar · **Parcial** roda, mas uma parte não é suportada ou não é comprovável sem instrumentação nova · **Não** o comportamento esperado não existe na POC (o cenário roda para provar a lacuna).

| Arquivo | Cenário | Requisito principal | Suporte |
|---|---|---|---|
| [CF-01](CF-01-replicacao-trafego-na-lider.md) | Tráfego na líder (baseline) | RPO | Sim |
| [CF-02](CF-02-replicacao-trafego-na-follower.md) | Tráfego na follower | RPO, HA | Sim |
| [CF-03](CF-03-trafego-distribuido-nas-tres-celulas.md) | Tráfego nas três células (carga padrão dos testes de falha) | RPO, HA | Sim |
| [CF-04](CF-04-leitura-forte-apos-escrita-em-outra-celula.md) | Leitura forte após escrita em outra célula | RPO | Sim |
| [CF-05](CF-05-capacidade-e-saturacao.md) | Capacidade e saturação | RPO, HA | Sim |
| [CF-06](CF-06-soak-longa-duracao.md) | Soak de 4 h (memória e disco com a compactação) | HA | Sim (Incremento A) |
| [CE-01](CE-01-troca-de-lideranca-abrupta-com-trafego.md) | Troca de liderança por comando | HA | **Não** (variantes c e d: Sim) |
| [CE-02](CE-02-lider-congelada-volta-como-zumbi.md) | Líder congelada volta como zumbi | RPO | Sim |
| [CE-03](CE-03-eleicao-sem-falha-por-cpu-ou-laco-lento.md) | Eleição falsa por CPU ou laço lento | HA | Parcial |
| [CE-04](CE-04-timeout-do-cliente-depois-do-commit-e-retry.md) | Timeout depois do commit e retry | RPO | Parcial (sem dedupe) |
| [CE-05](CE-05-follower-com-atraso-de-apply-vira-lider.md) | Follower atrasada no apply vira líder | HA | Sim (mede o RTO real) |
| [CE-06](CE-06-lider-antiga-volta-com-cauda-nao-confirmada.md) | Líder antiga volta com cauda não confirmada | RPO | Sim |
| [CE-07](CE-07-atualizacao-de-versao-em-rolagem.md) | Atualização de versão em rolagem | HA | Parcial |
| [FP-01](FP-01-pod-restart-uma-follower.md) | Reinício de 1 follower | FT | Sim |
| [FP-02](FP-02-pod-restart-todas-as-followers.md) | Reinício das 2 followers | RPO | Sim (sem HA, por desenho) |
| [FP-03](FP-03-pod-restart-lider.md) | Reinício da líder | HA | Sim |
| [FP-04](FP-04-pod-outage-10min-uma-follower.md) | 1 follower fora por 10 min | FT | Sim com CRG-1; Parcial com CRG-2 até o Incremento B (a follower volta atrás da cauda) |
| [FP-05](FP-05-pod-outage-10min-todas-as-followers.md) | 2 followers fora por 10 min | RPO | Sim (sem HA, por desenho) |
| [FP-06](FP-06-pod-outage-10min-lider.md) | Líder fora por 10 min | HA | Sim com CRG-1; Parcial com CRG-2 até o Incremento B |
| [FP-07](FP-07-reinicio-simultaneo-das-tres-celulas.md) | Reinício simultâneo das 3 | RPO | Sim |
| [FP-08](FP-08-no-ec2-perdido-e-volume-reanexado.md) | Nó EC2 perdido, EBS reanexado | HA | Sim no app; depende do EKS (volta longa com CRG-2: ver FL-02) |
| [FP-09](FP-09-oomkill-da-lider.md) | OOMKill da líder | HA | Sim (o OOM natural não deve mais acontecer) |
| [FP-10](FP-10-falha-dupla-follower-fora-e-lider-cai.md) | Follower fora e líder cai | RPO | Sim (sem HA, por desenho) |
| [FP-11](FP-11-lider-cai-durante-alcance-de-follower.md) | Líder cai durante alcance | RPO, HA | Sim; Parcial se a follower já estiver atrás da cauda |
| [FP-12](FP-12-perda-de-uma-az-inteira.md) | Perda de uma AZ | FT, HA | Sim |
| [FD-01](FD-01-problema-de-disco-na-follower.md) | Disco da follower | FT | Parcial |
| [FD-02](FD-02-problema-de-disco-na-lider.md) | Disco da líder | HA | Parcial |
| [FD-03](FD-03-disco-cheio.md) | Disco cheio (enchimento externo) | HA | Parcial (o teto duro cobre o WAL, não o enchimento externo) |
| [FD-04](FD-04-disco-lento-ou-no-teto-de-iops.md) | Disco lento / teto de IOPS do gp3 | HA | Sim |
| [FD-05](FD-05-volume-perdido-wal-zerado-com-mesmo-node-id.md) | Volume trocado, WAL zerado, mesmo NODE_ID | RPO | **Não** (inseguro); recusa no Incremento B (DC-12) |
| [FD-06](FD-06-wal-com-cauda-corrompida.md) | WAL com cauda corrompida | HA | **Não** (conferido no código: sem `wal.Repair`) |
| [FD-07](FD-07-restauracao-de-backup-antigo-do-wal.md) | Backup antigo do WAL restaurado numa célula ativa | RPO | **Não** (por desenho, P-04) |
| [FR-01](FR-01-particao-isolando-a-lider.md) | Partição isolando a líder | RPO, HA | Sim |
| [FR-02](FR-02-particao-isolando-uma-follower.md) | Partição isolando uma follower | FT | Sim; com 5 min e CRG-2, ver FL-02 |
| [FR-03](FR-03-particao-assimetrica.md) | Partição assimétrica | HA | Sim |
| [FR-04](FR-04-latencia-e-perda-entre-azs.md) | Latência e perda entre AZs | HA | Sim |
| [FR-05](FR-05-rede-oscilando.md) | Rede oscilando | HA | Sim |
| [FB-01](FB-01-dynamodb-lento-ou-inalcancavel-numa-follower.md) | DynamoDB fora numa follower | HA | Sim |
| [FB-02](FB-02-dynamodb-lento-ou-inalcancavel-na-lider.md) | DynamoDB fora na líder | HA | Sim (sem escrita, por desenho) |
| [FB-03](FB-03-erro-permanente-do-banco-credencial-ou-tabela.md) | Credencial negada / tabela inexistente | HA | Parcial |
| [FB-04](FB-04-escrita-direta-no-dynamodb-bypass.md) | Escrita direta no banco | RPO | **Não** |
| [FC-01](FC-01-chave-quente.md) | Chave quente | RPO | Sim |
| [FC-02](FC-02-itens-grandes.md) | Itens grandes | RPO | Sim, a confirmar no limite |
| [FC-03](FC-03-tempestade-de-retries-durante-failover.md) | Tempestade de retries | HA | Parcial |
| [FT-01](FT-01-salto-de-relogio-numa-celula.md) | Salto de relógio | medição | Sim |
| [FL-01](FL-01-compactacao-sob-carga-ram-e-disco-estaveis.md) | Compactação sob carga: RAM, disco e partida estáveis | HA | Sim (Incremento A) |
| [FL-02](FL-02-follower-atras-da-cauda.md) | Follower atrás da cauda da líder (`MsgSnap`) | RPO, FT | Parcial (fica parada sem cair; recupera no Incremento B) |
| [FL-03](FL-03-follower-fora-da-janela-reconstrucao.md) | Follower fora da janela: reconstrução | RPO, HA | **Não** (Incremento B) |
| [FL-04](FL-04-teto-duro-de-disco.md) | Teto duro do disco do log | HA | Parcial |
| [FL-05](FL-05-queda-durante-o-snapshot.md) | Queda no meio de um snapshot | RPO, HA | Sim |
| [FL-06](FL-06-identidade-e-configuracao-incoerente-na-partida.md) | Identidade do WAL e configuração incoerente | RPO | Sim |
| [FL-07](FL-07-progresso-atras-do-disco-retido.md) | Progresso do applier atrás do disco retido | RPO | Parcial (risco nas variantes c e d) |
| [RP-01](RP-01-caminho-de-replicacao-feliz.md) | Caminho da replicação: feliz | — | descrição |
| [RP-02](RP-02-caminho-de-replicacao-com-falha.md) | Caminho da replicação: falha | — | descrição |

## Ordem sugerida de execução

1. Ambiente (AMB) e oráculo (ORC) prontos; CF-01 a CRG-0 para sanidade.
2. **CF-05** (define a vazão máxima e, com ela, CRG-1 e CRG-2) e **CF-01** (baseline).
3. CF-02, CF-03, CF-04.
4. Lista original: FP-01 a FP-06, FD-01, FD-02, CE-01 (variantes c e d).
5. Rede: FR-01 a FR-05. Banco: FB-01 a FB-03.
6. Combinadas e eventuais: CE-02 a CE-07, FP-07 a FP-12.
7. Carga: FC-01 a FC-03, FT-01.
8. Lacunas, em ambiente isolado: CF-06, FD-03, FD-05, FD-06, FB-04, FD-07.
9. Log do Raft (acrescentados em 18/10, base `eec02c9`): FL-01 e FL-05 junto com o baseline; FL-02, FL-04, FL-06 e FL-07 depois das falhas de pod; FL-03 só com o Incremento B.

## Achados do código que mudam a leitura dos resultados

| Achado | Onde | Efeito na validação |
|---|---|---|
| ~~Não há compactação nem snapshot do log do Raft~~ **Resolvido no `eec02c9` (Incremento A)** | `etcdraft/compactor.go`, `logreader.go` | RAM e disco limitados, partida pela cauda (CF-06, FL-01). O que sobrou: uma célula atrás da cauda fica parada em `raft.atrasCauda` até o Incremento B (FL-02, FP-04 com CRG-2) |
| Volume novo sobe com o mesmo NODE_ID | `etcdraft/storage.go` (`openStorage` cria WAL vazio) | pânico em laço ou perda de dado confirmado numa sequência construída (FD-05) |
| `wal.Repair` não é chamado na partida | `etcdraft/storage.go` | cauda rasgada com dados impede a célula de subir (FD-06, conferido no código) |
| Item no limite de 400 KB sem espaço para o `_dr_v` | `dynamohttp/parser.go` | **Corrigido depois do `eec02c9`**: a borda reserva os bytes do `_dr_v` (FC-02) |
| Progresso antigo restaurado pularia entradas | `bootstrap/app.go` | **Corrigido depois do `eec02c9`**: a partida recusa um progresso atrás do disco retido (FL-07) |
| Laço do Raft preso não é visto pelas sondas nem pelas métricas | `/livez` sempre ok; `/readyz` depende só de conhecer a líder; tick e fsync são observados só depois que terminam | pausa de I/O na líder tira a escrita daquela entrada durante toda a pausa, sem o Kubernetes perceber (FD-02) |
| O caminho de escrita usa a líder vista pelo laço; `/status` e métricas usam a do motor do Raft | `etcdraft/node.go` (`lead` atômico) × `usecase/status.go` | durante um laço preso, o painel pode mostrar "follower" enquanto a escrita ainda age como líder (FD-02) |
| Erro de credencial ou tabela é tratado como item venenoso | `dynamodb/store.go` (`classify`) | uma mudança de IAM de minutos para a célula até ação humana (FB-03) |
| Sem dedupe de retry | `usecase/write.go` | cada nova tentativa é uma entrada nova; sobrescrita por retry atrasado em chave compartilhada (CE-04, FC-03) |
| Admissão só olha o apply da líder | `usecase/write.go` (`admit`) | follower pode ficar atrasada sem limite; o atraso entra no RTO quando ela vira líder (FP-04, CE-05) |
| Sem transferência de liderança | removida em `9710fc3` | SIGTERM da líder custa uma eleição inteira (CE-01, CE-07) |
| Log em Info a cada escrita encaminhada | `peerclient/client.go` | custo de CPU e de log na follower sob carga (CF-02) |
| Duração da escrita só medida para `ok` | `prommetrics/recorder.go` | latência de erros e PENDING invisível no servidor (M-09) |

# Análise do DataReplicationApp

## Resumo executivo

O `DataReplicationApp` é uma prova de conceito, escrita em Go, de uma camada de
replicação síncrona para dados compatíveis com DynamoDB. O cliente envia operações
para qualquer uma das três células; uma delas atua como líder, registra a operação
em um log Raft replicado e só confirma a escrita depois de cumprir as barreiras de
consenso, gate e aplicação local. As outras células materializam o mesmo log em seus
próprios bancos DynamoDB.

O objetivo não é implementar um banco novo. A aplicação fica entre o cliente e os
bancos por célula: oferece uma API HTTP compatível com parte do protocolo DynamoDB,
coordena a ordem das mutações por Raft e mantém as cópias das células convergindo.
O código contém estruturas para um gate adicional associado a learners (a arquitetura
prevista para a cópia Azure), mas a configuração Compose desta POC desliga esse
componente. Na demonstração local, a confirmação depende do quorum Raft das três
células AWS, não de uma confirmação entre AWS e Azure.

**Em uma frase:** é um serviço de replicação por log, com líder única por shard,
quorum Raft, leituras fortes e um adaptador DynamoDB limitado, executado localmente
com três células e três instâncias independentes do LocalStack.

## Que problema resolve

Sem uma camada coordenadora, duas ou mais cópias de um banco podem divergir quando há
falha de rede, troca de região ou escrita concorrente. Este serviço centraliza a
ordem das mutações em um log consensual: as réplicas recebem as mesmas entradas e as
aplicam com a mesma versão. A versão permite identificar e absorver a repetição de
uma entrada durante recuperação.

O modelo implementado é de **um shard lógico replicado por várias células**. O
processo de cada célula contém um nó Raft e um banco local. Uma implantação pode
configurar várias tabelas lógicas, mas o Compose de demonstração usa uma tabela de
cadastro por célula:

| Célula | API da aplicação | Administração | Banco de demonstração | Volume Raft |
|---|---:|---:|---|---|
| 1 | 8001 | 9091 | `localstack-1`, porta 4566 | `cell-1-raft` |
| 2 | 8002 | 9092 | `localstack-2`, porta 4567 | `cell-2-raft` |
| 3 | 8003 | 9093 | `localstack-3`, porta 4568 | `cell-3-raft` |

As três células são os três votantes Raft. Com três votantes, a maioria necessária
para commit é dois. Cada uma grava em seu próprio banco, configurado por
`DYNAMODB_ENDPOINT`; o armazenamento das tabelas não é compartilhado entre células.

## Arquitetura do código

O projeto usa uma separação hexagonal: adaptadores de protocolo e infraestrutura
ficam nas bordas, os casos de uso coordenam o fluxo e o domínio concentra tipos e
regras. As interfaces em `internal/port` permitem testar os casos de uso com doubles
sem depender diretamente do DynamoDB ou da biblioteca Raft.

| Camada | Responsabilidade | Principais arquivos |
|---|---|---|
| Entrada HTTP | Receber o protocolo HTTP/JSON de operações DynamoDB, validar e converter erros | `internal/adapter/in/dynamohttp/` |
| Entrada gRPC | Receber mensagens Raft, encaminhamento de escrita, barreira de leitura e publicação do gate | `internal/adapter/in/peergrpc/server.go`, `api/peerv1/` |
| Casos de uso | Escrita, leitura, aplicação, propagação de progresso/gate, degradação e status | `internal/usecase/` |
| Domínio | Entradas, versões, tabelas, gate, lease e regras de envio antes/depois da persistência | `internal/domain/` |
| Saída DynamoDB | Cliente AWS SDK, catálogo de tabelas e aplicação/leitura por célula | `internal/adapter/out/dynamodb/` |
| Saída Raft | Integração com `go.etcd.io/raft`, WAL do etcd e armazenamento em memória | `internal/adapter/out/etcdraft/` |
| Saída entre pares | Cliente gRPC para transporte Raft e chamadas entre células | `internal/adapter/out/peerclient/client.go` |
| Inicialização/configuração | Montagem das dependências, ciclo de vida, validação de ambiente | `internal/bootstrap/app.go`, `internal/platform/config/config.go` |
| Métricas | Exposição de séries Prometheus e coleta de estado | `internal/adapter/out/prommetrics/`, `observability/` |

`cmd/datareplication/main.go` é o ponto de entrada. Ele monta o grafo de produção
por meio de `bootstrap.NewProduction()`, inicia a aplicação Fx, aguarda sinal de
encerramento e tenta parar os serviços com prazo limitado. A composição concreta
das portas e adaptadores fica em `internal/bootstrap/app.go`.

## Fluxo de escrita

O caminho de uma escrita (`PutItem`) é, em termos simplificados:

1. **Entrada e validação.** O adaptador HTTP lê `X-Amz-Target`, interpreta o JSON e
   valida operação, tabela permitida, esquema de chave, atributos reservados e
   parâmetros suportados. O nome físico da tabela precisa ser exatamente o nome
   permitido para aquela célula. A validação acontece antes do caso de uso, então
   uma requisição inválida não cria uma entrada Raft.
2. **Normalização.** A requisição vira uma operação de domínio contendo tabela
   lógica, chave canônica e a imagem final do item. O cliente não pode definir
   `_dr_v`, que é reservado para a versão interna da replicação.
3. **Descoberta da líder.** O serviço de escrita consulta o estado local do Raft.
   Se não conhece líder, espera até `WAIT_LEADER` (limitando também o número de
   requisições aguardando). Se a célula atual é follower, encaminha a operação por
   gRPC à líder, no máximo uma vez.
4. **Admissão na líder.** Antes de propor, a líder confere o lease caso o shard esteja
   em modo degradado e verifica se o atraso local de aplicação passou
   `APPLY_LAG_MAX`. Se passou, devolve throttling antes de consumir um índice do log.
5. **Proposta e commit Raft.** A líder propõe uma entrada JSON ao Raft. O adaptador
   espera a confirmação da entrada por um `ProposalID`; o prazo esgotado é tratado
   como resultado desconhecido (`PENDING`), pois a proposta pode ter sido commitada.
6. **Persistência do Raft.** O laço `Ready` separa mensagens que podem sair antes do
   WAL das demais, grava o HardState e as entradas no WAL e faz o fsync pelo
   `wal.Save`. Em seguida atualiza o `MemoryStorage` e envia as mensagens que devem
   esperar pela persistência. Falhas do WAL são fatais para o processo; a POC não
   tenta continuar com estado de consenso incerto.
7. **Gate.** O ACK não avança além do gate. Na arquitetura com learners, a regra do
   domínio limita o gate ao menor entre o commit Raft e o índice confirmado por
   pelo menos dois de três learners Azure no termo corrente. Na Compose local,
   `LEARNERS` está vazio e `POC_SEM_AZURE=true`; o gate é o commit Raft.
8. **Aplicação no banco local da líder.** O `CommitPump` percebe o commit publicado
   pelo Raft, busca as entradas commitadas no `MemoryStorage` e as entrega ao
   `Applier`. O applier espera o gate, grava no DynamoDB da célula líder e só então
   avança sua marca d'água quando todas as operações daquele índice e os índices
   anteriores terminaram.
9. **Resposta.** A líder devolve sucesso apenas depois do gate, do apply local e da
   verificação final do lease. Inclui `x-dr-index` com o índice usado como versão.
   Follower que encaminhou recebe o resultado da líder e o repassa ao cliente.
10. **Replicação para as demais células.** Cada nó recebe o log Raft e o aplica em
    seu banco local. Esse apply nas followers é assíncrono em relação à resposta de
    escrita: um ACK não significa que todas as tabelas já foram atualizadas. O
    Compose e o guia mencionam atraso de até alguns segundos para a inspeção direta;
    as leituras pela API usam uma barreira forte para não expor esse atraso.

O laço Raft não espera o applier. O commit é publicado como um sinal, e o
`CommitPump`/applier trabalham fora do laço de `Ready`. Isso evita que um banco lento
prenda o processamento de heartbeats e cause eleições em série. Se o apply local
da líder ficar excessivamente atrasado em relação ao gate, novas escritas são
recusadas; entradas já commitadas continuam aguardando entrega/aplicação.

### Versionamento e idempotência

O índice da entrada Raft é também a versão do item. Ao materializar um `PutItem`, o
adaptador DynamoDB grava esse índice em `_dr_v` usando uma escrita condicional:
grava se o item não tem versão ou se a versão atual é menor. Se uma repetição/replay
encontrar versão igual ou superior, a falha condicional é tratada como “já aplicado”,
não como erro. As respostas `GetItem` e `Scan` removem `_dr_v` antes de devolver os
itens.

Isso torna a **reaplicação do mesmo log** idempotente, mas não equivale a deduplicar
uma nova requisição do cliente. Se uma chamada termina em `PENDING` e o cliente a
envia novamente, a repetição pode virar outra entrada, conforme os riscos descritos
no README.

### Aplicação paralela

O applier aceita `APPLY_WORKERS` workers (padrão 16). Um hash estável de tabela e
chave envia todas as alterações da mesma chave ao mesmo worker, preservando a ordem
do log para aquela chave. Chaves diferentes podem ser gravadas em paralelo.

Como operações de índices maiores podem acabar antes de operações anteriores de
outras chaves, `applied` é uma **marca d'água contígua**, não apenas o maior índice
que alguma chamada já gravou. Ela só avança quando todos os itens do índice e os
anteriores foram concluídos. Essa marca d'água é a referência para ACK, leitura forte
e retomada do applier.

Erros transitórios do banco são repetidos com backoff exponencial entre 100 ms e
5 s, sem limite de tentativas enquanto o processo estiver ativo. Um erro permanente
estaciona o applier naquela célula, sem saltar o item; `/readyz` passa a falhar e
`/status` expõe `erroApply`. Isso não encerra automaticamente o processo, mas impede
que a marca d'água ultrapasse o item defeituoso.

## Fluxo de leitura forte

`GetItem` e `Scan` também têm um caminho coordenado:

1. A célula descobre a líder.
2. Se a chamada chegou a uma follower, ela solicita à líder por gRPC uma barreira que
   contém, na mesma resposta, o `ReadIndex` do Raft e o gate.
3. A líder exige que o gate esteja pronto, usa `ReadIndex` para confirmar que ainda
   detém a liderança com quorum e lê o índice do gate depois dessa confirmação.
4. A célula que recebeu a leitura espera seu applier atingir
   `min(ReadIndex, gate)`.
5. Só então lê de seu próprio DynamoDB com `ConsistentRead=true`.

Portanto, a leitura não é enviada à líder para obter os dados: a líder fornece a
barreira, mas a célula local devolve os dados da própria réplica depois de alcançar
um ponto seguro. Cada chamada de `Scan` tem sua própria barreira; não há modo
eventual configurável nesta versão.

## Consenso, comunicação e durabilidade

- A implementação usa `go.etcd.io/raft/v3`; o pacote `etcdraft` é o único que importa
  diretamente a biblioteca de consenso.
- As mensagens do Raft são serializadas em protobuf e transportadas por gRPC entre
  células. A fila de saída é limitada por par; cheia, a mensagem é descartada sem
  bloquear o laço Raft e o nó reporta o par como inalcançável.
- Além do transporte do Raft, o gRPC carrega encaminhamento de escrita, pedido de
  barreira forte e publicação do gate.
- O serviço peer é descrito manualmente em `api/peerv1`, sem arquivo `.proto`
  gerado. O envelope gRPC usa um codec JSON próprio; o conteúdo das mensagens Raft
  continua sendo bytes protobuf.
- O WAL é persistente por nó/célula. O estado do Raft também é carregado para um
  `MemoryStorage` durante a execução. A POC cria um snapshot sintético inicial para
  armazenar a configuração, mas rejeita snapshots recebidos e deixa compactação
  desabilitada.
- Um arquivo separado guarda o progresso do applier junto ao volume Raft. Ele é
  gravado sem fsync; se perder, o sistema pode repetir mais entradas, cuja aplicação
  é protegida pela versão condicional.
- Ao reabrir um WAL, o processo confere identidade do nó e configuração de votantes
  e learners. Uma alteração de `PEERS`/`LEARNERS` incompatível com o WAL impede a
  partida; não há reconfiguração dinâmica de membros implementada.

## O gate e o modo degradado

O gate é o limite até onde ACKs, apply e leituras podem avançar. No caminho concebido
para produção, o quorum Azure usa o segundo maior índice durável entre três learners
(dois de três), limitado pelo commit Raft. O progresso é associado ao termo da líder:
uma troca de termo descarta os progressos antigos para reconstruir a confirmação no
termo novo, sem recuar o índice já liberado. Nas followers, a líder publica o gate
periodicamente; cada follower também o limita pelo próprio commit.

O domínio inclui política `accept` ou `reject`, `DEGRADE`/`RESTORE`/`RENEW` e lease.
O lease é calculado a partir do último contato comprovado com maioria dos learners,
com relógio monotônico; uma líder nova não herda contatos anteriores. O desenho
prevê que a entrada de degradação seja replicada no Raft e que a validade seja
conferida antes de confirmar cada escrita.

Há uma distinção importante entre esse desenho e a execução local:

- O `docker-compose.yml` define `LEARNERS=""` e `POC_SEM_AZURE=true`; assim, o gate
  real da POC não espera Azure.
- O Compose usa `DEGRADED_POLICY=reject`. A POC local não confirma escritas em modo
  degradado e não testa RPO diferente de zero.
- As regras do gate, do lease e parte do caso de uso de degradação têm testes de
  unidade, mas o README declara que o gate completo com Azure não é exercitado na
  POC.
- O código oferece métodos de `Restore` e `Renew`, mas a montagem runtime observada
  não expõe uma API administrativa para acioná-los. Também é necessário validar
  ponta a ponta o replay/adoção dessas entradas de controle antes de considerar
  o failover com modo degradado uma funcionalidade operacional completa.

Por isso, `POC_SEM_AZURE=true` deve ser entendido como uma chave explícita de
demonstração local, não como configuração de produção: desliga a proteção adicional
do gate Azure.

## Interface e escopo do protocolo

O endpoint público fala HTTP com corpo JSON no formato de protocolo DynamoDB,
identificando a operação por `X-Amz-Target`. A implementação aceita nesta POC:

- `PutItem`;
- `GetItem`;
- `Scan` da tabela base, com limite e chave de início.

`ConsistentRead` não habilita ou desabilita modos: as leituras já são fortes. A
resposta acrescenta headers internos como `x-dr-index` e, se aplicável,
`x-dr-mode`. Erros internos são convertidos para respostas compatíveis com o estilo
de erro DynamoDB, com códigos como `NO_LEADER`, `PENDING`, `READ_TIMEOUT`,
`GATE_NOT_READY` e `APPLIER_STOPPED`.

Operações não suportadas, parâmetros fora do escopo, GSI, tabelas não permitidas,
chaves inválidas e `_dr_v` enviado pelo cliente são rejeitados na borda. O README
indica `DeleteItem` e `BatchWriteItem` como fase futura; o domínio já tem alguns
tipos para delete, mas isso não significa que a API ou o adaptador de banco os
suportem hoje.

O serviço de administração, separado da API de dados, oferece:

- `/livez`: processo vivo;
- `/readyz`: tabelas conferidas, Raft rodando, líder conhecida e applier não
  estacionado;
- `/status`: retrato de líder, termo, commit, gate, aplicado, quorum, workers,
  atraso e estado do applier;
- `/metrics`: métricas Prometheus.

## Execução e observabilidade

O `docker-compose.yml` levanta três processos da aplicação, três LocalStacks, um
Prometheus e um Grafana. Prometheus coleta `/metrics` de cada célula a cada segundo;
Grafana provisiona dashboard e datasource. O `taskfile.yaml` automatiza a criação de
tabelas, escrita/leitura, inspeção direta dos bancos, cenários de falha e carga.

O `Dockerfile` tem uma etapa de build estático, uma etapa de testes com `go test
-race -count=1 ./...` e uma imagem runtime Alpine executada pelo usuário não-root
`dr`. O comando de testes é definido no Dockerfile/Taskfile; não foi executado como
parte desta análise.

As métricas cobrem liderança e quorum, índices de commit/gate/apply, operações e
latências HTTP, encaminhamentos, atraso de aplicação, fila, tentativas de apply,
fsync do WAL, intervalo de ticks e contrapressão. Isso permite diferenciar, por
exemplo, falta de líder, buffer Raft cheio e apply lento.

Configuração importante:

| Variável | Função e padrão relevante |
|---|---|
| `CELL_ID`, `NODE_ID`, `PEERS`, `TABLES` | Identidade local, membros e tabelas; obrigatórios ou validados na partida |
| `LEARNERS`, `POC_SEM_AZURE` | Learners de replicação extra; learners vazios só são aceitos com flag explícita da POC |
| `DEGRADED_POLICY` | Obrigatória (`accept` ou `reject`); Compose escolhe `reject` |
| `APPLY_WORKERS` | Concorrência do applier; padrão 16 |
| `APPLY_LAG_MAX` | Admissão por atraso do applier; padrão `APPLY_WORKERS × 256` |
| `ELECTION_TICKS` | Ticks até timeout de eleição; no Compose são 30 ticks de 100 ms |
| `REQUEST_DEADLINE`, `WAIT_LEADER`, `FORWARD_TIMEOUT` | Orçamento de tempo da API e encaminhamento, com relações validadas |
| `DYNAMODB_ENDPOINT`, `AWS_REGION` | Endpoint opcional (LocalStack) e região AWS |
| `RAFT_DIR` | Diretório persistente do WAL; Compose o monta em volume por célula |

## Cenários de falha previstos/testáveis

As tarefas documentadas exercitam encaminhamento por follower, reinício/indisponibilidade
de follower, eleição após parada da líder, escrita sem quorum, banco lento por célula
e rejeição de requisições inválidas. No modelo Raft, sem maioria as novas escritas não
devem receber confirmação. Uma resposta `PENDING` significa **resultado desconhecido**:
a proposta pode vir a ser commitada/aplicada após a recuperação, então não deve ser
interpretada como prova de que nada aconteceu.

No retorno de uma célula, o WAL e o progresso salvo permitem reconstruir o nó e
retomar a aplicação. As escritas condicionais pela versão tornam seguro repetir
aplicações já materializadas. As tarefas também distinguem inspeção direta do
LocalStack (que pode observar replicação ainda em andamento) de leitura pela API
(que aguarda a barreira forte).

## Limites e riscos que devem orientar a leitura do projeto

1. **É uma POC, não uma implantação pronta para produção.** O Compose é um ambiente
   local com serviços compartilhando CPU/disco; não prova latência nem disponibilidade
   de uma arquitetura multirregional real.
2. **Azure não participa do caminho executado pelo Compose.** O gate para ACK é o
   commit Raft AWS; as regras de learners Azure são cobertas principalmente por
   testes unitários até haver validação integrada.
3. **Sem TLS/autenticação no transporte peer da POC.** O cliente gRPC usa credenciais
   insecure e o serviço não apresenta uma camada de autenticação do protocolo
   descrita no código. Não exponha as portas a redes não confiáveis sem completar
   esses controles.
4. **Escopo DynamoDB reduzido.** Há apenas `PutItem`, `GetItem` e `Scan` de tabela
   base; não se deve assumir compatibilidade geral com DynamoDB, GSI, consultas,
   transações ou operações em lote.
5. **Retentativa do cliente não é deduplicação.** `PENDING` pode ser seguido de
   reenvio como uma segunda proposta; a versão absorve replay do mesmo índice, mas
   não elimina duas propostas diferentes.
6. **Sem compactação do log na POC.** O README identifica crescimento de memória/log
   como risco de uso prolongado. `MemoryStorage` também pode acumular atraso quando a
   aplicação não acompanha, especialmente num caminho degradado extenso.
7. **Aplicação pode estacionar por erro permanente.** O comportamento é deliberado
   para não saltar dados, porém exige diagnóstico e ação humana antes da célula voltar
   a ficar pronta.
8. **Falha após proposta é ambígua para o cliente.** `PENDING` preserva segurança
   evitando um falso sucesso, mas a resolução da ambiguidade precisa ser tratada pelo
   cliente/aplicação.
9. **A configuração de membros não é dinâmica.** WAL e `PEERS`/`LEARNERS` precisam
   permanecer coerentes; mudança de membership requer procedimento que a POC não
   implementa.
10. **Degradação precisa de validação ponta a ponta.** A lógica de domínio não deve
    ser confundida com uma operação operacional completa, principalmente para
    retomada após troca de líder, renovação humana e restauração.

## Como entender ou experimentar o repositório

1. Leia primeiro `README.md` para a promessa da POC e `HOW_TO_RUN.md` para subir o
   ambiente e executar cenários.
2. Leia `internal/adapter/in/dynamohttp/parser.go` e `handler.go` para ver o escopo
   efetivo da API e as respostas HTTP.
3. Siga `internal/usecase/write.go`, `replicate.go` e `apply.go` para entender a
   ordem entre proposta, gate e materialização.
4. Leia `internal/usecase/read.go` para a barreira forte e
   `internal/domain/gate.go`/`lease.go` para as regras de quorum e degradação.
5. Examine `internal/adapter/out/etcdraft/node.go` e `storage.go` para o laço Raft,
   persistência e recuperação; `internal/adapter/out/dynamodb/store.go` para a
   gravação condicional e as leituras consistentes.
6. Confira `internal/bootstrap/app.go`, `internal/platform/config/config.go` e
   `docker-compose.yml` para verificar como o que foi lido é realmente ligado e
   executado.

Para executar localmente, os comandos principais são `task start`, `task e2e`,
`task test`, `task status`, `task write`, `task read PK=...` e `task clean`, dentro
da pasta do projeto. Os pré-requisitos e cenários estão descritos em
`HOW_TO_RUN.md`.

## Conclusão

O projeto demonstra uma abordagem coerente de **escrita serializada por consenso +
apply local versionado + leitura forte com barreira**. Sua característica mais
importante é não confundir commit do log com materialização imediata em todos os
bancos: o ACK espera o quorum/gate e o apply da líder, enquanto followers continuam
aplicando em paralelo; uma leitura forte aguarda a réplica onde foi recebida alcançar
uma barreira segura.

O código também demonstra preocupações operacionais reais — fsync antes de confirmar
mensagens Raft apropriadas, filas limitadas, contrapressão, watermarks contíguas,
backoff, readiness, retomada e métricas. Porém, o ambiente local desativa Azure e
degradação, limita severamente a API e não inclui compactação ou controles de
transporte para produção. Assim, a leitura correta é: **POC funcional do mecanismo
de replicação entre três células locais; base técnica e domínio parcialmente
preparados para um modelo multicloud mais forte, mas não prova de que esse modelo
completo esteja operacional**.

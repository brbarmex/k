# ADR pendente — Tombstones para deleção e restauração do estado

> **Status:** proposta para spike; não aprovada para implementação.
> **Escopo:** investigar como preservar a semântica de DELETE durante replay,
> reconstrução e restauração de uma célula, especialmente depois de compactar o WAL.
> Esta ADR cruza a proposta técnica anexada com as decisões existentes do
> DataReplication e com o estado atual do `DataReplicationApp`.

> **Nota (18/10/2026): premissa alterada pela DC-11.** Esta ADR parte da reconstrução
> em tabela sombra da ADR-047. A DC-11 do canvas de compactação
> ([GGQPA-XXX-202610181712](../spdd/prompt/GGQPA-XXX-202610181712-[Feat]-service-compactacao-log-raft-recuperacao-reconstrucao.md))
> trocou isso: a reconstrução **sobrescreve no lugar** a tabela que a célula já tem,
> pelo `PutItem` condicional por `_dr_v`, e a tabela sombra e o apagar e recriar
> ficaram fora. Consequências para quando esta ADR for retomada:
> 1. A lápide deixa de ser contingência e vira **obrigatória** para o DELETE: sem ela,
>    um item apagado na fonte continua vivo na tabela da célula reconstruída.
> 2. A remoção segura de lápides precisa considerar a tabela velha de **qualquer
>    célula que ainda possa ser reconstruída**, inclusive uma parada há muito tempo.
>    Sem a saída "recomeçar do zero", ou a lápide é mantida para sempre, ou se define
>    um prazo além do qual uma célula parada não volta por sobrescrita.
> 3. As referências à ADR-047 como base da reconstrução passam a ser à DC-11.

## Resumo

O projeto já escolheu, para a v1 arquitetural, **DELETE real no banco**, sem
tombstone dentro do item, com operações da mesma chave aplicadas em ordem e
reconstrução por tabela sombra ([ADR-024](../doc/03_adr/ADR-024-delete-real-ttl-local-gsi.md),
[ADR-047](../doc/03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).
O ADR-047 deixa registrada como contingência uma **tabela separada de tombstones,
consultada somente em replay/rebuild**, caso os testes encontrem ressuscitação
indevida ("zumbi").

A proposta anexada descreve corretamente a necessidade de representar uma deleção
como evento imutável e posterior às escritas anteriores, mas mistura isso com
detalhes de um WAL próprio (LSN, timestamp, CRC) que não devem ser adotados sem
adaptação: o DataReplication usa o índice/termo do Raft e o WAL do etcd. Um evento
DELETE no log e um tombstone materializado para restaurar dados são conceitos
relacionados, mas **não são a mesma coisa**.

Recomendação para o spike: preservar DELETE real na tabela de serving e, se a
reconstrução precisar proteger-se contra cópias antigas sem o trecho correspondente
do log, avaliar um registro de tombstone separado, versionado pelo índice Raft e
consultado somente no rebuild/restore. Não habilitar TTL para remover tombstones até
que exista um low-water mark comprovando que nenhum WAL, snapshot, backup ou rebuild
válido pode reintroduzir estado anterior à deleção.

## Contexto do projeto

### Decisões arquiteturais existentes

- **ADR-024:** mantém DELETE real, sem tombstone; TTL do cliente é local a cada
  célula e não é replicado pelo log.
- **ADR-047:** formaliza a invariante `I-DEL` — com leituras abertas, o estado da
  célula deve ser a dobra do log até `applied` — e propõe reconstrução em tabela
  sombra com cópia difusa mais replay ordenado a partir de um índice `F`. Tombstone
  é uma alternativa fora da v1, preferencialmente em tabela separada e consultada
  apenas em replay/rebuild.
- **ADR-063:** ainda pendente, trata de snapshots/compactação e alerta que uma
  follower atrás do ponto compactado pode precisar de reconstrução. Um snapshot
  Raft apenas como marcador não repõe os dados que faltam no banco; o restore deve
  proteger o estado do banco e o índice anunciado.

### Implementação atual do `DataReplicationApp`

O código Go nesta POC ainda não implementa DELETE:

- `internal/domain/entry.go` define `KindWrite`/`KindNoop` e `OpPut`;
- `internal/adapter/in/dynamohttp/parser.go` rejeita `DeleteItem`;
- `internal/adapter/out/dynamodb/store.go` aceita `Put` condicional por `_dr_v` e
  classifica outras operações como erro permanente;
- a persistência do log é feita pelo WAL de `go.etcd.io/etcd/server/v3/storage/wal`,
  e o índice Raft é a versão usada no banco.

Portanto, a proposta de tombstone não descreve uma capacidade já disponível na POC.
Ela implica ampliar o formato de domínio, a API, o applier, o adaptador de banco,
o progresso/recuperação e os testes. O WAL deve continuar sob responsabilidade da
biblioteca Raft/WAL do etcd; não se recomenda introduzir outro LSN independente nem
um segundo WAL de mutações.

## Problema e hipótese

Um `DeleteItem` físico remove o item e também remove a versão `_dr_v` que estava
armazenada nele. No fluxo normal, a ordem por chave e o apply serializado impedem
que um PUT anterior seja aplicado depois de um DELETE posterior. A dificuldade está
em caminhos de recuperação que não reexecutam exatamente o mesmo histórico ordenado:

- restauração de uma tabela de backup/PITR;
- cópia de uma célula saudável para uma célula reconstruída;
- reconstrução por cópia difusa para uma tabela sombra;
- follower que ficou atrás de entradas já compactadas;
- recuperação do banco a partir de estado cuja versão seja anterior ao índice de
  deleção.

Nesses caminhos, a origem pode conter um item antigo que já havia sido excluído.
Como o DELETE físico apagou a versão no item, uma cópia ou replay inadequado pode
reintroduzi-lo. A hipótese é que um registro durável `(tabela lógica, chave, índice
de DELETE)` — mantido fora da tabela de serving — permita detectar e suprimir essa
versão antiga durante rebuild/restore, sem expor marcador em `GetItem`, `Scan` ou
outras leituras do cliente.

O spike deve demonstrar se esse registro é necessário além do protocolo já definido
no ADR-047, que retém o log desde `F` e reaplica `Put`/`Delete` em ordem. Não se deve
adicionar custo de tombstone ao caminho normal de escrita sem evidência de que a
invariante e a reconstrução em tabela sombra são insuficientes.

## Proposta para investigação

### 1. Representação da deleção no log Raft

Uma deleção futura deve ser uma **nova entrada imutável no log Raft**, posterior
às operações precedentes. Conceitualmente, a operação contém:

| Campo lógico | Uso |
|---|---|
| índice e termo Raft | Ordem total no shard e identidade da versão; o índice substitui um LSN paralelo |
| tipo `DELETE` | Distingue deleção de `PUT` |
| tabela lógica | Permite aplicar a operação na tabela física correspondente de cada célula |
| chave canônica | Identifica precisamente o item afetado |
| payload | Ausente para DELETE, salvo metadados operacionais necessários |

Não se deve editar ou remover o PUT antigo do WAL. A deleção é outro evento
commitado. A confirmação segue as barreiras já existentes: commit/gate e apply local
da líder antes do ACK. As followers aplicam a entrada depois, e leituras fortes
continuam condicionadas à marca d'água local.

**Formato ainda não decidido:** a proposta anexada sugere `timestamp` e `crc32` no
registro. No projeto, o WAL do etcd já armazena HardState, entradas e integridade do
formato de WAL. O spike deve avaliar se há necessidade de checksum ou timestamp
adicional no payload de negócio; não duplicar mecanismos sem requisito. Timestamp
de parede não deve substituir índice/termo Raft como critério de ordenação.

### 2. Projeção na tabela de serving

Manter como hipótese-base o **DELETE físico**, para não vazar marcadores à API nem
alterar semântica de Scan/Query, páginas, TTL ou DynamoDB Streams. O tombstone de
recuperação, se aprovado, fica numa estrutura separada por célula e não é retornado
ao cliente.

Um registro candidato seria:

```text
(cluster_id, geração_da_tabela, tabela_lógica, chave_canônica)
    -> (índice_delete, termo, metadados_de_auditoria)
```

O registro deve ser atualizado idempotentemente: para a mesma chave e geração, a
versão persistida não pode regredir. Durante rebuild, o item copiado da origem só
pode prevalecer se sua versão for posterior ao índice de DELETE conhecido. Se a
origem não expuser uma versão confiável, ela não é suficiente para resolver o
conflito; a reconstrução deve usar o log retido, outra fonte de autoridade ou
falhar/solicitar intervenção, em vez de assumir que o item é válido.

É preciso decidir como os tombstones coexistem com PUTs posteriores à deleção. A
comparação deve sempre ser por índice: um item gravado em índice maior do que o
tombstone é uma recriação válida; item de índice menor ou igual não pode ressuscitar.
Não remover um tombstone simplesmente porque ocorreu um PUT posterior sem antes
definir como cópias antigas e backups serão comparados.

### 2.1. Consistência entre log, item e registro auxiliar

O WAL Raft deve ser a fonte autoritativa da ordem das mutações; uma tabela auxiliar
de tombstones é uma projeção de recuperação, não um segundo log de consenso. Como
Raft e o DynamoDB não compartilham uma transação, o desenho precisa tratar quedas
entre o commit da entrada, o `DeleteItem` na tabela principal e a gravação do
tombstone auxiliar.

O spike deve definir a ordem e a condição para avançar `applied`: a marca d'água não
pode ultrapassar um DELETE enquanto os efeitos considerados obrigatórios para
restauração ainda não estiverem duráveis. Se a projeção auxiliar puder ser
reconstruída do WAL, registrar como detectar lacuna, reexecutar idempotentemente e
manter a célula cercada até completar. Se a garantia depender de escrita atômica no
banco, validar se o adaptador consegue gravar item e tombstone na mesma transação e
quais limites/custos isso impõe. Em nenhuma hipótese um erro de gravação do
tombstone pode ser tratado como sucesso silencioso.

### 3. Replay e restauração

O protocolo a validar para restaurar uma célula é:

1. identificar a geração, a configuração do shard e um ponto inicial seguro: índice
   de snapshot/checkpoint ou primeiro índice ainda retido;
2. escolher uma origem de dados e registrar o seu índice/versão de referência; uma
   cópia que não informa sua versão não deve ser considerada completa;
3. reconstruir em tabela sombra, sem abrir leituras na sombra;
4. reaplicar somente entradas Raft commitadas, em ordem crescente de índice, desde
   o ponto de referência `F`; respeitar o gate e a ordem por chave do applier;
5. considerar tanto PUTs como DELETEs e comparar versões, incluindo o índice de
   tombstone separado caso o trecho do log que contém o DELETE já tenha sido
   compactado;
6. alcançar a cabeça/limite de cutover, pausar ou cercar escritas conforme o
   protocolo, validar checksum por faixa e fazer cutover da geração;
7. liberar leituras somente depois que o estado da tabela ativa corresponda ao
   high-water mark anunciado; se faltar histórico ou houver divergência, manter a
   célula não pronta e reiniciar/replanejar o rebuild.

Não se deve reconstruir a partir de entradas não commitadas nem avançar `applied`
apenas porque o Raft aceitou um snapshot/metadado. O índice aplicado precisa
corresponder a dados efetivamente materializados no banco. Em caso de snapshot de
marcador e perda do trecho necessário, aplica-se o caminho de reconstrução previsto
no ADR-063.

### 4. Retenção e remoção segura de tombstones

Tombstone não pode expirar com um TTL arbitrário. Sua remoção só seria segura após
um ponto de corte global (low-water mark) que prove que nenhuma fonte ainda
utilizável pode fornecer estado anterior à deleção. O cálculo precisa considerar,
no mínimo:

- menor progresso relevante de votantes/learners que ainda possam voltar;
- snapshots e progresso de apply persistidos;
- reconstruções em andamento, incluindo o `F` do ADR-047;
- backups/PITR que a operação permite restaurar;
- retenção do WAL e procedimento de readmitir/reconstruir uma célula;
- geração do banco/tabela e histórico de cutover.

Se uma fonte restaurada estiver abaixo do low-water mark ou não tiver histórico de
deleção necessário, ela deve exigir full rebuild a partir de fonte autoritativa; não
pode ser aceita por replay incremental incompleto. A política de retenção deve
explicitar o custo de armazenamento contra a janela de restauração garantida.

## TTL passivo da proposta anexada: incompatibilidades e riscos

A proposta anexada também sugere guardar `expire_at` absoluto no log e, no replay,
**ignorar completamente cada entrada já expirada**. Isso não deve ser adotado sem
revisão:

1. **Conflita com ADR-024.** A arquitetura atual decide que o TTL do cliente é local
   a cada célula e não é replicado pelo log. Mudar para TTL avaliado no replay
   transforma expiração em semântica do log e exige nova decisão de produto e de
   consistência entre células.
2. **Ignorar uma versão expirada pode ressuscitar uma anterior.** Exemplo: o índice
   10 grava uma versão ainda válida; o índice 11 atualiza a mesma chave com TTL mais
   curto, que já expirou. Se o replay simplesmente pular o índice 11, o valor do
   índice 10 permanece no mapa. Para que a última operação continue sendo
   autoritativa, expiração precisa atuar como barreira/tombstone versionado para a
   chave, mesmo quando o payload já não é mantido.
3. **UTC não remove clock skew.** Um `expire_at` absoluto replicado é melhor que
   replicar uma duração relativa, mas avaliar `expire_at <= now` em relógios locais
   diferentes ainda pode produzir visibilidade divergente perto da fronteira. A
   sincronização NTP/PTP reduz o desvio, mas não torna a decisão determinística por
   si só.
4. **Snapshot filtrado precisa reter a barreira.** Não incluir o valor expirado no
   snapshot pode ser correto, mas o snapshot/checkpoint ainda precisa preservar a
   versão/estado de expiração suficiente para impedir que replay, cópia antiga ou
   backup reintroduza um valor anterior.

Recomendação: tratar TTL como uma decisão separada. Até uma ADR revisar explicitamente
o contrato de TTL local, não mudar a regra existente. Se um futuro TTL for
determinístico e replicado, avaliar converter a expiração em uma transição de estado
versionada/tombstone ou manter um `expire_at` autoritativo no estado mais recente,
sem descartar silenciosamente entradas durante replay.

## Alternativas a comparar

| Alternativa | Vantagens | Custos/riscos |
|---|---|---|
| DELETE real + replay ordenado e tabela sombra (ADR-047) | Sem marcador na tabela de serving; sem escrita extra por delete; mantém contrato atual | Exige reter log desde `F`; cópia/restauração sem histórico suficiente não é segura |
| Tabela separada de tombstones apenas para rebuild/restore | Bloqueia ressuscitação de cópias antigas sem alterar respostas públicas | Escrita/armazenamento extra por delete; precisa de retenção, versão, geração e processo de limpeza |
| Tombstone embutido no item de serving | Simples de consultar por chave e retém versão da deleção | Vaza ou exige filtrar Get/Scan/índices; afeta TTL existente, paginação, GSIs e Streams; conflita com ADR-024 |
| Recriar estado exclusivamente a partir do WAL completo | Sem tabela auxiliar de tombstones | WAL sem compactação cresce; após compactação/backups antigos não resolve sozinho; rebuild caro |

## Perguntas em aberto para o spike

1. O protocolo de cópia difusa + replay do ADR-047 e a retenção `F` bastam para
   todos os casos suportados, tornando desnecessária uma tabela de tombstones?
2. Quais fontes de restauração são suportadas: tabela de outra célula, PITR/backup,
   snapshot de estado ou replay integral do WAL?
3. Que versão acompanha a origem copiada? Como decidir quando a tabela restaurada
   não contém `_dr_v` ou a versão é de outra história/geração do log?
4. O tombstone deve ser mantido na tabela de dados auxiliar por chave, por faixa,
   ou como estado compactado no snapshot? Como permanece consistente com o commit
   Raft e com a falha entre gravações em bancos diferentes?
5. Qual low-water mark permite remover tombstones sem invalidar backups ou rebuilds
   ainda suportados?
6. A limpeza de tombstones terá uma janela operacional configurável ou será
   controlada pela retenção de snapshots/backup? Qual é o comportamento quando uma
   célula volta depois dessa janela?
7. O TTL continuará local como ADR-024, ou existe requisito para expiração
   determinística replicada? Como impedir a divergência por relógios?
8. Como verificar formalmente I-DEL e ausência de zumbis em sequências concorrentes
   de PUT/DELETE, queda em cada etapa, cutover e replay parcial?

## Critérios de saída do spike

- Matriz de fontes de restauração e garantias de histórico/versão de cada uma.
- Demonstração reproduzível de que um item apagado não reaparece após crash/replay,
  rebuild de tabela sombra, restauração PITR e retorno de uma follower além da
  retenção.
- Testes de property/modelo para PUT/DELETE/replay e para versões antigas e novas
  da mesma chave.
- Política de low-water mark e retenção que cobre logs, snapshots, backups e
  rebuilds em andamento, com falha fechada quando a cobertura é insuficiente.
- Contrato claro de TTL, separado da semântica do DELETE replicado.
- Estimativa do custo de storage e writes da tabela auxiliar, se tombstones forem
  necessários.
- Plano de integração para o código Go: domínio/entrada Raft, parser, applier,
  adaptador DynamoDB/Cloud-Adapter, armazenamento auxiliar, progresso, observabilidade
  e compatibilidade de snapshot.

## Referências

- Proposta técnica anexada: restauração de WAL com tombstone e TTL passivo.
- [ADR-024 — DELETE real, TTL local e GSI](../doc/03_adr/ADR-024-delete-real-ttl-local-gsi.md)
- [ADR-047 — Invariante de DELETE e reconstrução em tabela sombra](../doc/03_adr/ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)
- [ADR-063 — Snapshot e compactação do log Raft](../doc/03_adr/ADR-063-snapshot-e-compactacao-do-log-do-raft.md)
- [Arquitetura: dados, versão, DELETE e TTL](../doc/01_arquitetura/05_dados_versao_delete_ttl_gsi_e_banco_por_celula.md)
- [C33 — Reconstrução em tabela sombra](../doc/02_cenarios/atualizados/C33-reconstrucao-em-tabela-sombra.md)
- `DataReplicationApp/internal/domain/entry.go`
- `DataReplicationApp/internal/adapter/in/dynamohttp/parser.go`
- `DataReplicationApp/internal/adapter/out/dynamodb/store.go`
- `DataReplicationApp/internal/adapter/out/etcdraft/node.go`
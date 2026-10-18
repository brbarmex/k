# ADRs pendentes — DataReplication

> **Status:** hipótese para spike; ainda não é uma decisão arquitetural aprovada.
> **Objetivo:** registrar o risco de crescimento do WAL Raft e orientar a investigação
> de uma estratégia segura de compactação, snapshots e recuperação.

## ADR pendente: retenção e compactação do log Raft em alta volumetria

### Hipótese do problema

Na implementação atual, as entradas commitadas são persistidas no WAL Raft de cada
célula e carregadas em `raft.MemoryStorage`. O log inclui as entradas de escrita
(`domain.Entry`, com a imagem do item) e metadados/estado de consenso. A POC não
executa snapshots periódicos nem compactação: o snapshot sintético de bootstrap
registra a configuração inicial, mas não substitui o histórico acumulado. O código
também não oferece o ciclo completo de envio, recebimento e instalação de snapshots
para recuperar uma réplica cujo log necessário já tenha sido descartado.

Com uma taxa sustentada de escritas, o WAL em disco e as entradas mantidas em memória
podem crescer ao longo do tempo. O efeito provável é aumento de consumo de disco e
memória, maior tempo de recuperação/replay após reinícios e, se o volume se esgotar,
falha ao persistir novas entradas Raft. A perda de disponibilidade de votantes pode
remover o quorum e interromper novas confirmações. O tamanho e a velocidade desse
crescimento ainda precisam ser medidos com cargas representativas; não se deve
assumir um limite operacional sem medições.

O banco por célula contém o estado materializado atual, enquanto o WAL contém o log
necessário ao consenso e à recuperação incremental. A existência do estado no
DynamoDB não torna seguro apagar o WAL: é necessário provar que o estado persistido,
o índice do snapshot e a configuração Raft correspondem, inclusive após falhas
durante a criação ou instalação do snapshot.

### Distinção importante: linearização e compactação

O Raft já estabelece uma ordem consensual para as entradas commitadas. A
linearizabilidade das operações observáveis depende também do protocolo de ACK,
das barreiras de leitura e do apply local; compactar o log, por si só, não fornece
linearização nem deve alterar essas garantias.

O spike deve verificar explicitamente que a política de snapshots e truncamento
preserva a ordem commitada, a semântica das leituras fortes e o comportamento de
escritas em andamento durante eleição, reinício e falha. Deve também esclarecer o
uso do termo “linearização” no requisito: a garantia visada para operações de
cliente, o ponto em que uma escrita passa a ser considerada confirmada e o
comportamento de respostas `PENDING`/resultado desconhecido.

### Recomendações para investigação (spike)

1. **Medir antes de escolher limites.** Medir bytes de WAL por entrada e por
   operação, taxa de crescimento em disco e memória, tempo de startup/replay e
   impacto de replicação atrasada. Projetar cenários para escrita contínua,
   atualizações repetidas da mesma chave, itens grandes e períodos de indisponibilidade.
2. **Definir o ponto seguro do snapshot.** Avaliar o uso da marca d'água contígua
   `applied` como limite para o estado materializado e confirmar como capturar um
   estado consistente com o índice e o `ConfState` Raft. Não usar apenas o maior
   índice commitado se o apply local ainda estiver atrás.
3. **Projetar persistência atômica e recuperação.** Definir como gravar e validar o
   snapshot, registrar seu índice/termo/configuração, ordenar a persistência do
   snapshot antes da compactação e recuperar de interrupções em cada etapa. A
   recuperação deve saber se continua do snapshot, do WAL posterior ou de uma cópia
   do banco; inconsistências precisam falhar explicitamente, sem avançar o progresso.
4. **Truncar WAL e memória como um único ciclo operacional.** A estratégia precisa
   limitar tanto os segmentos do WAL em disco como as entradas do
   `MemoryStorage`. Apagar segmentos sem atualizar o estado de recuperação, ou
   compactar apenas a memória, não resolve o crescimento integralmente.
5. **Cobrir réplicas atrasadas.** Se uma follower precisar de entradas anteriores ao
   primeiro índice retido, avaliar transferência de snapshot, instalação segura e
   retomada da replicação. O comportamento atual para `MsgSnap` não implementa essa
   recuperação, portanto a compactação não deve ser habilitada isoladamente.
6. **Definir o que o snapshot representa.** Decidir se o snapshot conterá os dados
   materializados, uma referência a um snapshot consistente do armazenamento por
   célula, ou outro mecanismo de reconstrução. Considerar que cada célula tem banco
   independente e que uma célula pode precisar recuperar tanto o log Raft quanto o
   seu estado de dados.
7. **Preservar a semântica das escritas e leituras.** Testar que uma escrita não
   recebe ACK antes das condições já exigidas (commit/gate e apply local), que
   leituras fortes continuam aguardando sua barreira e que operações em curso não
   são perdidas nem confirmadas duas vezes por efeito da compactação. A deduplicação
   de uma nova tentativa de cliente é uma questão distinta da idempotência do replay
   do mesmo índice.
8. **Definir política operacional.** Escolher frequência/limiar do snapshot, margem
   de retenção do log, alertas de espaço e atraso, comportamento sob snapshot lento
   e procedimento para restaurar uma célula. Quantificar o custo de armazenamento e
   de transmissão para snapshots grandes.
9. **Validar falhas e compatibilidade.** Testar crash durante geração, persistência,
   compactação, envio e instalação; disco cheio; corrupção; follower muito atrasada;
   reinício simultâneo; troca de líder; e versões incompatíveis de snapshot. Confirmar
   a compatibilidade com a versão de `go.etcd.io/raft` usada pelo projeto e com o
   formato do WAL.

### Opções a comparar

- **Snapshots periódicos do estado aplicado + compactação do Raft:** potencialmente
  reduz o histórico retido, mas exige um mecanismo confiável de captura/instalação e
  recuperação do estado de cada célula.
- **Snapshots mais frequentes ou por limiar de tamanho/índice:** podem limitar o
  pico de crescimento, ao custo de mais I/O, CPU e interferência na latência.
- **Retenção de uma janela de entradas além do snapshot:** preserva margem para
  followers com atraso moderado; requer dimensionamento por taxa de escrita e
  duração de falhas tolerada.
- **Reconstrução do banco a partir de outra réplica ou backup:** pode evitar embutir
  todos os dados no snapshot Raft, mas precisa de protocolo de consistência e de
  recuperação independente do log já compactado.

Estas opções são candidatas para análise, não recomendações finais. A escolha deve
ser sustentada pelos requisitos de RPO/RTO, tamanho do estado, taxa de mutação,
capacidade de transferência e comportamento esperado durante falhas.

### Critérios de saída do spike

- Medições de crescimento em disco/memória e recuperação para perfis de carga
  representativos.
- Definição verificável do índice seguro para snapshot e da relação com `applied`,
  `commit`, gate e `ConfState`.
- Fluxo documentado de criação, persistência, compactação, envio/instalação e
  restauração, incluindo falhas em cada transição.
- Testes demonstrando que a recuperação não perde entradas commitadas, não aplica
  estado incompatível com o índice anunciado e preserva as garantias de escrita e
  leitura forte.
- Limites, métricas, alertas, retenção e procedimento operacional de recuperação
  propostos e revisados antes de qualquer ativação em ambiente real.

### Referências no código

- `DataReplicationApp/internal/adapter/out/etcdraft/node.go`: persistência do WAL,
  tratamento atual de `MsgSnap` e loop do Raft.
- `DataReplicationApp/internal/adapter/out/etcdraft/storage.go`: abertura e
  recuperação do WAL; snapshot sintético de bootstrap.
- `DataReplicationApp/internal/usecase/replicate.go`: entrega das entradas
  commitadas ao applier.
- `DataReplicationApp/internal/usecase/apply.go`: watermark `applied` e progresso
  de aplicação.
- `DataReplicationApp/internal/adapter/out/dynamodb/store.go`: materialização
  versionada no banco independente de cada célula.
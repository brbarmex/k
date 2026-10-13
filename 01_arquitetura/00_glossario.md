# Glossário e códigos de referência

[Início](../00_COMECE_AQUI.md) · [Documento principal](../DataReplication_Versao_1.md)

## Estrutura e componentes

| Termo | Significado |
|---|---|
| **DataReplication** | O app único de cada célula. Recebe as chamadas do AWS SDK (API compatível com DynamoDB), encaminha escritas à líder, participa do Raft, mantém os dois logs e aplica no banco da célula. Não há sidecar |
| **Célula** | Um cluster Kubernetes (EKS ou AKS) em **uma** AZ, com as apps do cliente e um `DataReplication` no mesmo namespace. Há 3 células na AWS (A1 a A3) e 3 na Azure (Z1 a Z3) |
| **Shard** | Unidade de ordenação e de consenso: um log, uma líder. No v1 a ordenação é por shard |
| **AZ / região** | AZ: datacenter independente dentro de uma região. Aqui: AWS `sa-east-1` e Azure `Brazil South`, ambas em São Paulo |
| **Compass-Router** | Roteador externo que leva cada cliente a uma célula. Fora do escopo do time |
| **Agent** | Processo do control plane que pede ao Compass-Router para mudar o destino do tráfego. Fora do escopo |
| **Cloud-Adapter** | Componente que traduz o formato do DynamoDB para o Cosmos DB (inclui expressão condicional e TTL). Outro componente, não faz parte do `DataReplication` |
| **Applier** | Rotina que grava as entradas commitadas no banco da célula, com escrita condicional por versão. Um por tabela por célula |
| **CommitPump** | Rotina que recebe as entradas commitadas e as distribui ao gate, ao applier e ao log de replicação |
| **Link dedicado** | Conexão privada (cabo físico) entre AWS e Azure, sem internet |
| **`drctl`** | CLI operacional: reconstruir réplica, transferir liderança, inspecionar, promover |

## Consenso e replicação

| Termo | Significado |
|---|---|
| **Raft** | Protocolo que faz os nós concordarem sobre a ordem das entradas e sobre quem é a líder. Biblioteca: `go.etcd.io/raft/v3` |
| **Votante** | Nó que participa da eleição e do commit. Só as 3 células AWS |
| **Learner** | Nó que recebe o log e informa o índice durável, mas **não vota**. As 3 células Azure |
| **Líder / follower** | Líder: a célula que propõe entradas. Follower: encaminha escritas à líder (1 salto) |
| **Termo / epoch** | Geração da liderança. Termo: do Raft. Epoch: da configuração do grupo (promoção) |
| **Gate** | Índice até onde ACK e applier podem avançar: `min(commit do Raft, 2º maior índice durável dos learners)`. Nunca recua. No modo degradado, ignora a Azure |
| **Log do Raft** | Log do consenso, de vida curta, com fsync antes do ACK |
| **Log de replicação** | Log nosso, de vida longa, gravado em lote depois do commit; serve a Azure atrasada, o `drctl` e a reconstrução |
| **Segmento / `segfile`** | Arquivo pré-alocado com CRC32C e `fdatasync`; motor compartilhado pelos dois logs |
| **Eleição / heartbeat / PreVote / CheckQuorum** | Escolha de nova líder; sinal de vida da líder; proteções do Raft contra eleições à toa e líder isolada |
| **Fencing** | Impedir que um escritor antigo continue gravando depois de perder o cargo, por termo, epoch ou versão |
| **Split-brain** | Duas partes aceitando escritas ao mesmo tempo, cada uma achando que é a líder |
| **TLA+ / Jepsen** | TLA+: especificação formal do protocolo. Jepsen: teste que injeta falhas de rede, disco e relógio |

## Escrita e leitura

| Termo | Significado |
|---|---|
| **Entrada** | Unidade replicada do log. Um `BatchWriteItem` vira uma entrada (ADR-035, C23) |
| **Índice** | Posição da entrada no log; também é a **versão** do item no banco |
| **Post-image** | O item como fica depois da escrita; é o que vai para o log |
| **`_dr_v`** | Atributo reservado (nome a definir) com a versão do item. O applier grava só se a versão guardada for menor |
| **Commit** | Entrada gravada com fsync em 2 de 3 votantes AWS |
| **ACK** | Resposta de sucesso ao cliente: depois do gate e do apply no banco da célula da líder |
| **Apply** | Gravação da entrada no banco (DynamoDB ou Cosmos) |
| **`appliedIndex`** | Último índice aplicado por inteiro no banco local |
| **Strong / Eventual** | `WRITE_MODE`: Strong aplica em série depois do gate; Eventual aplica em paralelo e o ACK sai só depois da Azure. `READ_MODE_DEFAULT`: padrão forte |
| **ReadIndex** | Leitura forte: a líder confirma que ainda é líder e informa o índice que a leitura precisa alcançar |
| **Dedupe** | Reconhecer o retry de uma escrita pelo id estável do SDK, numa janela de 5 min, para não aplicá-la duas vezes |
| **`PENDING`** | `503` quando o prazo estoura depois do commit: a escrita não se perdeu e o retry é absorvido pelo dedupe |
| **`REQUEST_DEADLINE` / `DEGRADE_AFTER`** | Prazos: 7,5 s da requisição e 2 s de espera pela Azure antes do modo degradado |
| **TTL / GSI / Scan** | Usos dos clientes: expiração por item (local por célula), índice global secundário (forte só até o índice propagar), varredura (sem snapshot) |

## Falhas, recuperação e operação

| Termo | Significado |
|---|---|
| **RPO / RTO** | Dado confirmado que pode se perder / tempo para voltar. RPO = 0 em operação normal; RTO = Delta-T (a medir) |
| **Modo normal / degradado** | Normal: o gate exige a maioria Azure (RPO 0). Degradado: a Azure está inalcançável e a AWS confirma sozinha (RPO > 0), sinalizado por `x-dr-mode: degraded` e métrica |
| **`DEGRADE` / `RESTORE`** | Entradas replicadas que mudam a regra do gate para degradado e de volta |
| **Episódio de degradação** | Período contínuo em modo degradado; seu `fromIndex` define a janela de RPO > 0 |
| **Ressincronização / catch-up** | A Azure recebendo o que perdeu até alcançar a AWS |
| **Promoção / `ForcePromote`** | Tornar a Azure líder quando a AWS cai. Sempre decidida por uma pessoa |
| **Failover / failback** | Passar a operação para a outra nuvem / voltar |
| **Escritas órfãs** | Escritas ACKadas na AWS durante o modo degradado que não chegaram à Azure antes da promoção |
| **Entrada venenosa** | Entrada commitada que o banco recusa sempre e que trava o applier |
| **Falha cinzenta** | Lentidão ou perda parcial em que nada está formalmente fora, mas tudo piora |
| **Flapping / histerese** | Cair e voltar repetidamente / exigir estabilidade por um tempo antes de mudar de estado |
| **Throttling / 429 / RU / WCU** | Banco recusando por carga / código HTTP / unidades de capacidade do Cosmos / do DynamoDB |
| **mTLS / IAM / IRSA** | TLS com autenticação dos dois lados / permissões da nuvem / papel IAM por carga de trabalho no EKS |
| **PITR** | Restaurar o banco como estava num instante passado |
| **Game day** | Simulação planejada de desastre para treinar e medir |

## Códigos usados nos documentos

| Código | O que é | Onde |
|---|---|---|
| **C01 a C31** | Cenário **atualizado** (modelo v1) | [`02_cenarios/atualizados/`](../02_cenarios/atualizados/) |
| **X01 a X06** | Cenário **complexo**, detalhado em arquivo próprio | [`02_cenarios/complexos/`](../02_cenarios/complexos/) |
| **R03 a R23** | Cenário ou hipótese **a rever** (os 14 que ainda não têm decisão) | [`02_cenarios/a_rever/`](../02_cenarios/a_rever/) |
| **ADR-001 a 043** | Registro de decisão de arquitetura | [`03_adr/`](../03_adr/INDEX.md) |
| **D1 a D13** | Decisões da revisão de 12 e 13/10 (D9 gate, D10 header, D11 dedupe, D12 TLA+, D13 prazos) | [`ADR`](../03_adr/INDEX.md) correspondentes |
| **PEND-nn** | Pendência com dono e padrão até a decisão | [`06_pendencias_e_riscos/`](../06_pendencias_e_riscos/01_pendencias.md) |
| **H1 a H11, H-DR1 a H-DR6** | Hipótese de falha em aberto | [R23](../02_cenarios/a_rever/R23-hipoteses-gerais.md) |
| **S1 a S7** | Spikes de medição (RTT, fsync, parse, libs) | [05_implementacao/06_testes_e_spikes](../05_implementacao/06_testes_e_spikes.md) |
| **Seções 5.x, 6.x, 9.x** | Numeração herdada do documento de arquitetura, preservada nos arquivos de `01_arquitetura/` | Mapa em [00_COMECE_AQUI](../00_COMECE_AQUI.md#mapa-de-seções-da-arquitetura) |

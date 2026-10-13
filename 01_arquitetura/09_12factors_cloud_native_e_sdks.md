# 12 Factors, cloud-native (AKS e EKS) e SDKs suportados

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 10. 12 Factors: o que se aplica e onde há desvio

| Fator | Módulo `api` | Módulo `core` (WAL) | Observação |
|---|---|---|---|
| I. Codebase | 1 repositório, 1 binário | idem | Dois papéis por subcomando/flag |
| II. Dependências | `go.mod`, build estático | idem | Sem CGO; imagem *distroless* |
| III. Configuração | Variáveis de ambiente | idem | Segredos por volume montado, nunca em env nem em log |
| IV. Serviços de apoio | Trata o `DataReplication` como recurso anexado das apps do cliente | O banco (DynamoDB/Cosmos) é recurso anexado | |
| V. Build/release/run | Imagem imutável por versão | idem | Configuração separada da imagem |
| VI. Processos | **Sem estado** | **Desvio**: WAL em volume persistente | Inevitável. O volume é o "estado", tratado como recurso anexado e descartável pelo desenho (6.6) |
| VII. Port binding | Porta própria (API local, gRPC, admin) | idem | |
| VIII. Concorrência | Escala por pods | **Não escala por réplicas**: o nº de réplicas é o quórum | Escala é por shards/nós, não por HPA |
| IX. Descartabilidade | Partida < 1s, parada limpa | Partida depende do WAL; parada limpa transfere liderança | |
| X. Paridade dev/prod | Mesmo binário, harness de simulação local | idem | |
| XI. Logs | `stdout`, JSON estruturado (`log/slog`), sem dado de negócio | idem | Itens do DynamoDB nunca entram em log |
| XII. Processos administrativos | — | `drctl` (subcomando/Job): reconstruir réplica, transferir liderança, inspecionar WAL | |

**Mensagem honesta:** o `node` não é um app 12-Factor puro, porque é um sistema com estado. O 12-Factor vale para a configuração, os logs e o ciclo de build do app inteiro, mas o módulo `core` (WAL em volume) é um sistema com estado e foge do fator VI.

### 10.1 Configuração: YAML com viper e variável de ambiente obrigatória (decisão do Bruno, 12/10)

**Decisão:** o app carrega um arquivo **YAML** com a lib **viper**; o modo de escrita vem de uma **variável de ambiente obrigatória**; o app não sobe se ela faltar.

**Cuidados para isso funcionar bem:**
1. **Falhar cedo e de forma explícita.** Validar todos os campos obrigatórios na partida (modo de escrita, `CELL_ID`, lista de tabelas, endereços dos pares) e sair com erro claro. Sem valores padrão para campos que mudam o comportamento de dados.
2. **O viper aceita chaves desconhecidas em silêncio.** Um erro de digitação no YAML passaria despercebido. Usar leitura estrita (`UnmarshalExact` ou decode com erro para chaves não usadas) para recusar chaves desconhecidas.
3. **Ordem de precedência explícita:** variável de ambiente > YAML > (nenhum padrão para os campos críticos). Documentar o nome da variável de cada campo.
4. **Segredos fora do YAML.** Certificados e credenciais vêm de arquivos montados, nunca do YAML nem de variáveis de ambiente.
5. **Sem recarga a quente na v1.** Mudar o modo de escrita ou a lista de tabelas exige reiniciar o pod, em rolling update de **uma célula por vez** (a mesma regra da manutenção). Desligar o `WatchConfig` do viper.
6. **Imprimir a configuração efetiva** (sem segredos) na partida e exportá-la como métrica de informação, para auditar divergências entre células.
7. **Divergência entre células:** campos que mudam a semântica do log (lista de tabelas, versão do formato) → o nó **recusa entrar** no grupo; campos que mudam só o comportamento do líder (modo de escrita) → alerta, sem recusar, porque durante o rolling update haverá divergência temporária.
8. **Peso do viper:** traz muitas dependências e algum consumo de memória e tamanho de binário. É aceitável nesta meta (256 MiB), mas medir no spike S2. Alternativas mais leves: `koanf`, ou `yaml.v3` com `KnownFields(true)` mais leitura manual das variáveis.


---

## 11. Cloud-native em AKS e EKS

- **Mesmos manifests** (Helm ou Kustomize) com *overlays* mínimos por nuvem. Diferenças reais: classe de armazenamento (EBS CSI vs Azure Disk CSI), identidade, balanceador e nome do volume.
- **Identidade por carga de trabalho, sem chave estática:** IRSA ou EKS Pod Identity (AWS) e Azure Workload Identity (AKS).
- **Impor a passagem pelo DataReplication:** a política IAM deve permitir que **só** o papel do `DataReplication` escreva no DynamoDB. Se a aplicação ainda tiver permissão de escrita direta, qualquer atalho **contorna a replicação** e quebra o RPO = 0. Esta é uma das proteções mais baratas e mais importantes.
- **mTLS entre todos os componentes**, inclusive entre nuvens. Não há identidade de nuvem compartilhada entre AWS e Azure, então usar CA própria/cert-manager ou SPIFFE/SPIRE, com rotação sem reinício (recarregar certificados).
- **Volumes zonais:** o PV de EBS e o de Azure Disk são presos a uma AZ. Como cada célula é um cluster numa única AZ, isso não atrapalha: se um nó morrer, o pod volta em outro nó do mesmo cluster com o mesmo volume. Se a AZ/cluster inteiro cair, aquela réplica fica fora até voltar ou até ser reconstruída (6.6).
- **Descoberta entre as células AWS (decisão do Bruno: headless Service + mesma VPC).** O headless Service resolve só dentro do próprio cluster, então não basta. Como os 3 clusters EKS estão na mesma VPC e os pods têm IPs da VPC (CNI padrão), o tráfego é roteável direto. Proposta para a v1: cada célula publica **um nome DNS estável** numa zona privada do Route 53, apontando para um NLB interno (alvo por IP, uma AZ) ou para o IP do pod via external-dns com TTL curto. O Raft guarda só o ID do membro, nunca o endereço. Liberar a porta de replicação nos security groups entre as 3 células. Reresolver o DNS com intervalo curto no gRPC, porque isso conta no RTO. A Azure continua por link privado e endpoint próprio.
- **Rede entre células:** o `DataReplication` expõe a porta de replicação por balanceador interno ou private link (um por cluster). A NetworkPolicy do namespace precisa de exceção só para essa porta e para a saída até o banco.
- **Criptografia em repouso** do volume do WAL com chave gerenciada: o WAL contém dado de negócio (valores monetários).
- **Rede entre as nuvens:** link privado e não-internet (premissa A5). Fora do escopo do app, mas todos os timeouts dependem do RTT medido.
- **Imagem:** *distroless* estática, multi-arquitetura (amd64 e arm64: Graviton e Cobalt costumam ser mais baratas), assinada (cosign), com SBOM e verificação (`govulncheck`) no CI.

---

### 11.1 Clientes suportados: somente AWS SDK v2 (decisão do Bruno, 12/10)

**Esclarecimento do Bruno (12/10):** os clientes precisam usar a **versão mais recente** do SDK da própria linguagem e **nunca a primeira geração** dele. A regra abaixo (geração mínima por linguagem) é a forma de aplicar isso. 

**Decisão:** todos os clientes usam o AWS SDK **v2 ou posterior ("v2+")**. Qualquer versão anterior à geração v2 é **rejeitada**. A regra vale por geração e por linguagem (Java 2.x e posteriores, Go v2 e posteriores, JavaScript v3 e posteriores etc.), sem versão mínima de patch, a menos que os testes de compatibilidade mostrem que alguma 2.x inicial não funciona.

**Cuidado com "ou posterior":** uma futura geração maior de SDK pode mudar o protocolo (por exemplo, outra codificação do corpo). Versões **mais novas que as testadas** devem entrar em modo **só alerta** (aceitar e registrar na métrica) até passarem na suíte de compatibilidade, em vez de serem aceitas sem teste.

**Pontos a esclarecer, porque "v2" não significa a mesma coisa em todas as linguagens:**
| Linguagem | Qual é o SDK "atual" |
|---|---|
| Java | AWS SDK for Java **2.x** (`software.amazon.awssdk`); o 1.x é o antigo |
| Go | AWS SDK for Go **v2** (`aws-sdk-go-v2`); o v1 é o antigo |
| JavaScript/Node | O atual é o **v3**; o **v2** é o antigo (o oposto das outras) |
| Python | boto3/botocore, sem "v1/v2" |
| .NET | O atual é o v4 (decisão do Bruno), sem "v2" |

**Linguagens dos clientes (Bruno, 12/10): Java, Go e .NET.** Regra por linguagem, com os cuidados de nomenclatura:
| Linguagem | Aceitar | Rejeitar | Armadilha |
|---|---|---|---|
| Java | AWS SDK for Java **2.x** (`aws-sdk-java/2.*`) | 1.x (`aws-sdk-java/1.*`) | — |
| Go | AWS SDK for Go **v2** (`aws-sdk-go-v2`) | v1 (`aws-sdk-go/`) | O "v2" está no **nome do módulo**; os números de versão do v2 são **1.x**. A regra deve casar pela família no `User-Agent`, não pelo número 2 |
| .NET | AWS SDK for .NET **somente v4** (Bruno, 12/10) | v3 e anteriores | **Não existe "SDK v2" atual em .NET**: o pacote `AWSSDK.DynamoDBv2` tem "v2" no nome porque é a versão da **API do DynamoDB**, não do SDK. |

**Decisões do Bruno (12/10):** .NET aceita **somente a geração mais recente (v4)**; requisição sem `User-Agent` reconhecível é **rejeitada**, depois de um período inicial (2 a 4 semanas) só com alerta; SDKs mais novos que os testados entram em modo só alerta até passarem na suíte.

**Como rejeitar versões antigas:**
1. **Identificar pelo cabeçalho `User-Agent`**, que os SDKs enviam (por exemplo, prefixos `aws-sdk-java/2.` e `aws-sdk-go-v2/`). Funciona como proteção contra erro de configuração, **não como segurança**: o cabeçalho pode ser falsificado ou alterado por frameworks que acrescentam texto. Casar por trecho, não por igualdade.
2. **Lista de famílias e versões mínimas em configuração** (YAML, 10.1), por exemplo: `java: >=2.x.y`, `go-v2: >=1.x.y`. O Bruno precisa informar a versão mínima de cada.
3. **Resposta da rejeição:** um erro HTTP 400 com tipo e mensagem claros (`UnsupportedClientVersion`, indicando a versão mínima). Usar um tipo que os SDKs tratem como **erro de cliente, sem retry**, para não gerar uma tempestade de retentativas.
4. **Métrica `rejected_client_version{sdk,version}`** para encontrar apps que ainda usam versão antiga antes de a regra ser ativada. Começar em modo **só alerta** por um período e depois passar para rejeitar.
5. **Sem `User-Agent` reconhecível:** rejeitar (política restritiva), ou aceitar atrás de uma chave de configuração para ferramentas internas. A decidir.

**Suíte de compatibilidade (X3):** testar PutItem, BatchWriteItem, DeleteItem e as leituras nas três linguagens (Java 2.x, Go v2, .NET v4), na versão mais antiga e na mais recente de cada uma, contra o `DataReplication` e contra o DynamoDB real, comparando cabeçalhos (`x-amz-crc32`, `x-amzn-RequestId`), formato de erros e comportamento de retry.

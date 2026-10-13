# Comunicação entre células: gRPC

> Detalhe de arquitetura do **DataReplication v1**. A numeração das seções (ex.: 5.3, 6.8) vem do documento de origem e é usada nas referências cruzadas; o mapa está em [00_COMECE_AQUI](../00_COMECE_AQUI.md). Visão geral e decisões: [documento principal](../DataReplication_Versao_1.md).

## 4. Protocolo entre os apps: gRPC?

**Recomendação: sim, gRPC, com disciplina de uso.** Para a aplicação continua HTTP/JSON compatível com o DynamoDB (o SDK não muda).

### 4.1 Por que gRPC
- Contrato tipado (protobuf), geração de código, *deadlines* e cancelamento propagados, interceptors para mTLS/observabilidade/auth, HTTP/2 multiplexado.
- É o padrão de fato em Go e em Kubernetes, com bom suporte em AKS/EKS e service meshes (se um dia houver).

### 4.2 Como usar (é aqui que se ganha ou perde performance)
| Regra | Motivo |
|---|---|
| **Um stream bidirecional por par de nós** para as mensagens Raft, com lotes de mensagens por *flush* | Uma chamada unária por mensagem Raft custa alocação, cabeçalhos e goroutine por mensagem. |
| **Um stream por origem interna** para propostas, multiplexando várias requisições com `requestId` | Evita 1 handshake de stream por transação. |
| Canal separado (conexão ou stream) para **replicação** e para **tráfego de cliente/forward** | Uma rajada de clientes não pode atrasar heartbeats. |
| Codec rápido (ex.: `vtprotobuf`) e *buffer pool* | Reduz alocação e pressão no GC. Verificar no spike. |
| Compressão **desligada** no caminho quente | CPU extra sem ganho: o link é privado e a vazão é baixa. |
| `keepalive` agressivo e janela de flow control ajustada | Detectar link morto em segundos, não em minutos. Evitar travar por janela pequena em RTT maior. |
| Tamanho máximo de mensagem alinhado ao limite do log (A4) | Evita memória sem limite. |

### 4.3 Riscos reais do gRPC aqui
- **Head-of-line blocking do TCP**: perda de pacote no link AWS↔Azure trava todos os streams da conexão. Mitigação: mais de uma conexão por par (heartbeat e dados separados). Alternativa futura: QUIC.
- **Memória por conexão/stream** no grpc-go (buffers, goroutines). Entre células o número de conexões é pequeno e fixo (quórum). Só a API HTTP das apps locais gera muitas conexões; limitar e medir.
- **Descoberta de líder e balanceamento**: o gRPC LB padrão não conhece o líder. A aplicação fala com o `DataReplication` da própria célula e ele encaminha. Sem LB inteligente na v1.

### 4.4 Alternativas descartadas
| Alternativa | Por que não na v1 |
|---|---|
| TCP puro com framing próprio | Menor sobrecarga, mas o custo de segurança (TLS, keepalive, backpressure) é todo nosso. Revisitar se o profiling mostrar o gRPC como gargalo. |
| HTTP/JSON interno | Mais lento e sem streaming nativo. |
| QUIC/HTTP3 | Atraente para o link entre clouds, mas imaturo no ecossistema Go de produção para este uso. |

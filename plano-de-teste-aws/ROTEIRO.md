# Roteiro dos cenários de teste na AWS

Seis cenários que validam replicação, encaminhamento, falha de follower, troca de líder, reconstrução e durabilidade da POC do DataReplication, rodando nos 3 clusters EKS do `infra-aws/`. Cada cenário é uma task (`task cenario:N`) que injeta a falha, mede e termina com um veredito **PASS** ou **FAIL** tirado de um observador independente do app: as 3 tabelas lidas direto no DynamoDB, confrontadas com o que o cliente recebeu como confirmado.

Tudo roda a partir desta pasta:

```bash
cd plano-de-teste-aws
task            # lista as tasks
```

| Arquivo | O que é |
|---|---|
| `Taskfile.yaml` | As etapas: infra, conexão, Grafana, os 6 cenários e derrubar. Inclui o `infra-aws/Taskfile.yaml` como `infra:*` |
| `scripts/cenario-N.sh` | A orquestração de cada cenário |
| `scripts/lib.sh` | k6 desacoplado do terminal, parar/voltar célula, fechamento com oráculo |
| `scripts/drtool_aws.py` | Status das células, escritas, espera de alcance, comparador das tabelas e oráculo do RPO (só biblioteca padrão) |

Os resultados (ledger do k6, logs, eventos, `top`) ficam em `infra-aws/resultados/<cenario>/`, fora do git.

---

## 1. Análise: os cenários contra o que está implantado

| # | Cenário pedido | Coerente? | O que mudou na implementação e por quê |
|---|---|---|---|
| 1 | 10 escritas na líder, replicadas nas 3 | **Sim** | O ACK da líder não espera o apply das followers (AP-09): a verificação espera a quiescência antes de ler as tabelas |
| 2 | 3 escritas na follower, com forward para a líder | **Sim** | O forward é provado pela métrica `dr_forwards_total` da follower (+3), não só pelo 200. A escrita usa a tabela da própria follower (`cadastro_cliente_cell_N`); outra tabela dá 400 |
| 3 | 500 TPS na líder; uma follower para no 1º minuto por 3 min; acaba quando ela alcança | **Sim, com 2 ajustes** | (a) "Stop" no Kubernetes é `scale --replicas=0`: apagar o pod não serve, porque o StatefulSet o recria na hora. (b) "Alcançar" sob carga é estado `normal` e atraso ≤ 1 s de entradas (`TOL`), já que o commit da líder não para. 3 min a 500 TPS = ~90 mil entradas, acima do `SNAPSHOT_EVERY_ENTRIES` (10 mil): a volta passa por `recuperando` (CatchUp), não só por MsgApp |
| 4 | 300 TPS; a líder cai, outra assume; volta depois de 1 min e alcança | **Sim, com 1 ajuste** | A carga entra **pelas followers**. O k6 escolhe os alvos no início e não redireciona: entrando pela líder, 100% da carga falharia durante a queda. A líder antiga volta como **follower**: o Raft não devolve a liderança |
| 5 | Apagar todos os registros do Dynamo e forçar um recovery | **Não como foi descrito** | Ver abaixo. Implementado como: apagar a tabela de **uma follower** e recuperar por `POST /admin/reconstruir` |
| 6 | Pane no disco ("queda de luz") para validar durabilidade | **Sim, com escopo definido** | "Queda de luz" vira um reboot imediato do nó pelo `sysrq-b`, sem sync nem desmontar. A durabilidade é provada pelo oráculo (nenhuma escrita confirmada perdida) e, na follower, pelo WAL relido ≥ o `match` que a líder via |

### Por que o cenário 5 precisou mudar

1. **A camada não detecta um apagamento feito direto no DynamoDB** (FB-04). Apagar os itens não "gera" recovery nenhum: as células continuam em `normal`, a leitura forte pela célula apagada devolve "não existe", e só um comparador externo percebe.
2. **O banco é o estado** (DC-06). O snapshot do Raft é só um marcador, sem dados, e o log é compactado. Se as **3** tabelas forem apagadas, não sobra fonte de onde copiar: **perda definitiva**. Não é um teste de recovery, é a demonstração de uma lacuna.
3. **O mecanismo de recuperação de dados que existe é a reconstrução** (DC-11): uma follower copia as tabelas de uma célula em `normal` (`CopyTable`), reinicia e alcança pelo CatchUp. Ela é disparada por `POST /admin/reconstruir` ou, sozinha, quando a follower sai da janela de retenção.
4. **A líder não se reconstrói** (409). Para testar a tabela da líder apagada, é preciso antes mover a liderança. Isso é o cenário 5b, manual (seção 4.5).

### Pontos de atenção que valem para todos

- **Capacidade.** 500 TPS é o baseline planejado, não um número medido. Rode `task rampa` antes: se o joelho ficar abaixo de 500, o cenário 3 vai mostrar `ThrottlingException`. Isso é recusa segura e o oráculo aceita, mas a meta de carga não terá sido atingida.
- **Tolerância a falhas zerada durante as falhas.** Com uma célula fora, sobram 2 de 3 votantes: qualquer segunda falha deixa o grupo sem quórum.
- **O DNS entra no tempo de volta.** Quando o pod some, o ExternalDNS remove `cell-N-0.cell-N.dr.internal` e só o republica quando o pod volta (intervalo de 10 s mais TTL de 5 s). Isso aparece no MTTR.
- **Oráculo.** O `infra-aws/README.md` lista o oráculo do RPO como pendente. Aqui ele foi implementado de forma enxuta (`drtool_aws.py oraculo`, regras R1 a R4 do ORC-04): imprime o resultado, mas ainda não publica as séries `dr_val_*` no Prometheus (ORC-05).

---

## 2. Pré-requisitos (uma vez)

1. Credencial AWS do `user-cli` no `~/.aws` (validada: conta `449484245754`, sem SCP).
2. Ferramentas e repositórios do Helm: `task preparar` (Terraform ≥ 1.9, AWS CLI v2, kubectl, Helm ≥ 3.18, Docker, Python 3).
3. `infra-aws/terraform/conta/terraform.tfvars` com `email_alertas`, depois `task conta` e confirmar o e-mail do SNS.
4. Service Quotas em sa-east-1: on-demand padrão ≥ 8 vCPU e spot ≥ 2 vCPU.
5. Imagem da POC **linux/arm64** publicada, anotada por digest:
   `docker buildx build --platform linux/arm64 -t ghcr.io/<você>/datareplication-poc:<tag> --push ../DataReplicationApp`

## 3. Sessão: subir, conectar, observar

```bash
task subir IMAGE=ghcr.io/<você>/datareplication-poc@sha256:...   # ~25 min; autodestruição em 3,5 h
task conectar        # OUTRO terminal, deixe aberto: túneis 19090 (Prometheus), 1909N (admin), 1800N (API)
task grafana         # http://localhost:3001 (dashboard DataReplication)
task status          # 1 líder, mesmo termo, commit = gate = aplicado
task pods            # nada em Pending nos 3 clusters
task rampa           # opcional, recomendado antes do cenário 3
```

Os cenários gravam **anotações no Grafana** (T0, volta, alcance): o intervalo da falha aparece marcado nos painéis.

Úteis durante a sessão: `task logs CELL=N`, `task estender HORAS=1`, `task ip` (o IP mudou e o kubectl parou).

**Ordem sugerida:** 1, 2, 5, 3, 4, 6 (`task cenarios:todos`). O 5 vem antes dos cenários com carga para a cópia da reconstrução ser pequena.

---

## 4. Cenários

Todos começam por `ping` (os túneis do `conectar` respondem) e descobrem a líder na hora: depois do cenário 4 ou do 6, a líder muda, e os seguintes se adaptam.

### 4.1 Cenário 1: escritas pela líder replicadas nas 3 células

```bash
task cenario:1            # QTD=10
```

| Passo | O que acontece |
|---|---|
| 1 | Descobre a líder pelo `/status` |
| 2 | Grava `QTD` itens novos pela API da líder (`localhost:1800N`), com pk `cli#c1-<execução>-...`; imprime HTTP e `x-dr-index` de cada um |
| 3 | Espera as 3 células com `aplicado ≥ commit da líder` por 10 s (quiescência) |
| 4 | Lê as 3 tabelas direto no DynamoDB e aplica o oráculo |

**Aceite:** 10/10 com HTTP 200; R1 (cada confirmada nas 3 tabelas com `_dr_v ≥ x-dr-index`) e R4 (mesmas pk com o mesmo `_dr_v` nas 3).
**No Grafana:** `dr_writes_total{resultado="ok"}` na líder; `dr_commit_index`, `dr_gate_index` e `dr_applied_index` iguais nas 3.

### 4.2 Cenário 2: escritas pela follower, com forward para a líder

```bash
task cenario:2            # QTD=3; CELL=N para escolher a follower
```

| Passo | O que acontece |
|---|---|
| 1 | Escolhe uma follower e lê o `dr_forwards_total` dela |
| 2 | Grava `QTD` itens pela API da follower, na tabela da própria follower |
| 3 | Lê o `dr_forwards_total` de novo: tem de ter subido `QTD` |
| 4 | Quiescência e oráculo, como no cenário 1 |

**Aceite:** 3/3 com HTTP 200; `dr_forwards_total` +3 na follower; R1 e R4.
**No Grafana:** "Encaminhamentos" na follower; latência da follower acima da da líder (um salto gRPC a mais).

### 4.3 Cenário 3: follower fora por 3 minutos sob 500 TPS

```bash
task cenario:3            # TPS=500 AQUECIMENTO=60 FORA=180 MODO=graceful TOL=<TPS> CELL=N
```

| Tempo | O que acontece |
|---|---|
| 0 s | k6 começa: `TPS` na líder (`ENTRADA=<N da líder>`), rodando dentro do pod, desacoplado do terminal |
| 60 s | **T0:** `kubectl scale statefulset cell-F --replicas=0`. O PVC fica: mesmo WAL, mesma identidade |
| 60 s a 240 s | Líder com 2 de 3 votantes; `dr_peer_lag{par=F}` cresce ~500 por segundo |
| 240 s | A follower volta (`--replicas=1`). Fica fora do `/readyz` até alcançar |
| até alcançar | `recuperando` (CatchUp a partir de outra célula) → `normal` com atraso ≤ `TOL`. **O cenário termina aqui** e mede o tempo de alcance |
| fim | k6 para (SIGINT, grava o resumo); quiescência; coleta; oráculo sobre o ledger inteiro |

`MODO=abrupto` troca o stop gracioso (SIGTERM, 25 s para fechar) por remoção forçada (≈ SIGKILL).

**Aceite:** RPO = 0 (R1 a R4); termo **constante** (sem eleição: a líder nunca perde o quórum); a follower passa por `recuperando` e volta a `normal`; o tempo de alcance fica registrado (RTO-07).
**No Grafana:** `dr_peer_lag`, `dr_quorum_active` (3 → 2 → 3), linha 11 (estado da célula, vazão do CatchUp, WAL retido), p99 de escrita na líder durante a falha (deve ficar perto do baseline).

### 4.4 Cenário 4: a líder cai sob 300 TPS e volta depois de 1 minuto

```bash
task cenario:4            # TPS=300 AQUECIMENTO=60 FORA=60 MODO=abrupto POS=30
```

| Tempo | O que acontece |
|---|---|
| 0 s | k6 começa: `TPS` pelas **duas followers** (`ENTRADA=follower`), que encaminham à líder |
| 60 s | **T0:** a líder é removida à força (`MODO=abrupto`: scale 0 e delete com `--grace-period=0`) |
| T0 + ~1 a 2 s | Eleição (`ELECTION_TICKS=10` = 1 s). Na janela sem líder, as escritas voltam 503 `NO_LEADER` (recusa segura) ou `PENDING` (incerta) |
| T0 + 60 s | A líder antiga volta, **como follower** |
| até alcançar | Ela alcança a nova líder; depois, mais `POS` s de carga para ver a latência estabilizar |
| fim | k6 para; quiescência; coleta; oráculo |

**Aceite:** RPO = 0 (R1 a R4; cada INCERTA está nas 3 tabelas ou em nenhuma); uma líder por termo; tempo de eleição (impresso, RTO-03) e Delta-T de escrita (primeira confirmação depois do T0 no ledger, RTO-04); a antiga alcança e fica como follower.
**No Grafana:** `dr_leader_changes_total`, `dr_raft_term` (+1), `dr_throttled_total{causa="nao_lider"}`, buraco nas confirmações por segundo.

### 4.5 Cenário 5: a tabela de uma follower apagada, recuperada pela reconstrução

```bash
task cenario:5            # SEMENTE=200 TPS=50 CELL=N (pede confirmação)
```

| Passo | O que acontece |
|---|---|
| 1 | Semente: `SEMENTE` itens pela líder; quiescência |
| 2 | Pré-condição: as 3 tabelas **inteiras** iguais (se não estiverem, o cenário para) |
| 3 | **T0:** apaga **todos** os itens de `cadastro_cliente_cell_F` direto no DynamoDB (scan e `batch-write-item`, sem passar pela camada) |
| 4 | Mostra a lacuna: a leitura forte pela follower devolve vazio, a líder devolve o item, e o comparador acusa a divergência. O app não acusa nada |
| 5 | k6 com `TPS` leve pela líder; `POST /admin/reconstruir` na follower → 202 |
| 6 | `reconstruindo` (CopyTable da fonte, sobrescrevendo no lugar) → reinício → `recuperando` → `normal` |
| 7 | k6 para; oráculo sobre a semente e a carga; as 3 tabelas inteiras iguais de novo; a leitura forte pela follower devolve o item |

**Aceite:** a reconstrução é aceita (202) e termina em `normal`; R1 a R4 sobre a semente e sobre as escritas feitas **durante** a cópia; tabelas inteiras iguais.
**No Grafana:** linha 11 (estado, itens copiados, pinos de disco na fonte). A escrita não para durante a reconstrução: a follower continua encaminhando à líder.

**5b, a tabela da líder (manual).** A líder recusa a própria reconstrução (409). Sequência: apagar a tabela da líder → `task cenario:4` com `FORA=10` (ela cai e volta como follower) → `python3 scripts/drtool_aws.py reconstruir <N>` → `esperar-estados <N> 1800 reconstruindo,recuperando` → `task comparar`. Enquanto ela for líder com a tabela vazia, as leituras fortes por ela devolvem "não existe": registre isso como lacuna (FB-04).

**Não rode com as 3 tabelas apagadas** esperando recuperação: não existe fonte, e os dados se perdem (seção 1).

### 4.6 Cenário 6: corte de energia no nó de uma célula, durabilidade

```bash
task cenario:6            # ALVO=lider|follower TPS=300 (pede confirmação)
```

| Passo | O que acontece |
|---|---|
| 1 | Sobe o pod privilegiado `falha` (`infra-aws/falhas/netshoot.yaml`) no nó da célula-alvo |
| 2 | k6 com `TPS`: pelas followers se o alvo é a líder; pela líder se o alvo é uma follower |
| 3 | Com `ALVO=follower`, registra o `match` dela visto pela líder: o que ela já confirmou ter gravado com fsync |
| 4 | **T0:** `echo b > /proc/sysrq-trigger` no nó: o kernel reinicia **na hora**, sem sync e sem desmontar. O que não passou pelo fsync se perde, como numa queda de luz |
| 5 | Com `ALVO=lider`: espera a nova líder |
| 6 | Espera o nó voltar (NotReady → Ready); o kubelet sobe a célula de novo no mesmo EBS |
| 7 | A célula alcança; lê a linha `partida: WAL relido` do log |
| 8 | **FD-01 A6** (só com `ALVO=follower`): o maior índice relido do disco tem de ser ≥ o `match` do passo 3. Se for menor, a célula confirmou o que não guardou: **falha de segurança** |
| 9 | k6 para; quiescência; coleta; oráculo |

**Aceite:** RPO = 0 (R1: nenhuma escrita confirmada perdida); A6 OK; a célula volta pelo mesmo volume e alcança. Com `ALVO=lider`, também os tempos do cenário 4.
**No Grafana:** buraco nas métricas da célula durante o reboot; `dr_wal_fsync_duration_seconds` antes e depois; node-exporter do nó (disco e CPU).

**Limites deste cenário.** No EBS, uma escrita confirmada pelo volume já é durável, então o corte prova a disciplina de fsync do app, mas não um disco que mente sobre o fsync (cache volátil). Isso pediria `dm-flakey` ou `dm-log-writes`, fora da POC. A **pausa de I/O** (FD-01 variante a) fica como variante manual: `task infra:falha:shell CLUSTER=N` e `fsfreeze -f` / `fsfreeze -u` no ponto de montagem do PVC; nesse caso o `/readyz` continua 200, que é justamente a lacuna que o FD-01 descreve.

---

## 5. Verificação avulsa

```bash
task comparar                       # as 3 tabelas inteiras iguais
task comparar PREFIXO='cli#c3-'     # só as pk de um cenário
task oraculo CENARIO=c3-1024-153012 # o oráculo de novo sobre um ledger já coletado
python3 scripts/drtool_aws.py ler 2 'cli#c1-...'   # o que o cliente veria pela célula 2
```

Classes do oráculo (ORC-02):

| Classe | Resposta | O que se exige |
|---|---|---|
| CONFIRMADA | 200 | nas 3 tabelas, com `_dr_v ≥ x-dr-index` (R1) |
| RECUSADA_SEGURA | 4xx, ou 503 com `x-dr-code` `NO_LEADER`, `THROTTLING` ou `CELL_RECOVERING` | em tabela nenhuma (R2) |
| INCERTA | 503 `PENDING`, 500, timeout, conexão cortada | nas 3 ou em nenhuma (R3) |

## 6. Derrubar (sempre ao fim)

```bash
task derrubar     # snapshot do Prometheus → remove os charts e os PVCs → destroy → confere órfãos ("Limpo.")
```

A stack `conta` (Budgets e autodestruição) fica. Se você esquecer, a autodestruição apaga node groups, clusters, tabelas e volumes vencidos, mas rode o `derrubar` mesmo assim: a VPC e a zona privada só saem pelo destroy. Depois, `task infra:obs:analise` sobe um Prometheus local sobre o snapshot para revisar os painéis.

## 7. Tempo e custo de uma sessão

| Etapa | Tempo aproximado |
|---|---|
| `subir` | 25 min |
| Cenários 1 e 2 | 2 min |
| Cenário 5 | 5 a 10 min (depende do tamanho das tabelas) |
| Cenário 3 | 7 a 9 min, mais o scan do oráculo |
| Cenário 4 | 4 a 5 min |
| Cenário 6 | 6 a 10 min (o reboot do nó leva de 1 a 4 min) |
| `derrubar` | 15 min |

Infra: ~US$ 0,62/h. DynamoDB: ~360 mil escritas de cliente nos cenários com carga (cada uma vira 3 PutItem), mais os scans do oráculo, dá ~US$ 1 a 2. Uma sessão de 2 h com os 6 cenários fica em **~US$ 3 a 4**.

# Como rodar a POC

Passo a passo para subir as três células, escrever, ler e testar os cenários de falha. O que a POC prova e as decisões de desenho estão no [README](README.md).

## 1. Pré-requisitos

| Ferramenta | Versão | Como conferir |
|---|---|---|
| Docker com Compose v2 | Docker Desktop 4.x ou equivalente | `docker compose version` |
| Task | v3 | `task --version` |
| AWS CLI | v2 | `aws --version` |
| curl | qualquer | `curl --version` |

Não é preciso Go no host: o build e os testes rodam em container.

Instalar o Task no macOS:

```bash
brew install go-task
```

Se o build falhar com `docker-credential-desktop: executable file not found in $PATH`, coloque os binários do Docker Desktop no PATH:

```bash
export PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH"
```

Nenhuma credencial real é usada: o taskfile já define `test`/`test` e a região `sa-east-1`.

## 2. Subir e conferir

Todos os comandos rodam dentro desta pasta (`DataReplicationApp/`).

```bash
task limpar   # começa de volumes vazios
task subir    # build da imagem, compose, tabelas e espera o /readyz das três células
task status   # uma célula "lider", as três com o mesmo lider e termo, e "azure":"off"
```

O `subir` leva cerca de 30 s com a imagem já construída. O primeiro build demora mais.

| Serviço | Porta no host |
|---|---|
| API (DynamoDB) da célula N | `800N` (8001, 8002, 8003) |
| Administração da célula N (`/livez`, `/readyz`, `/status`) | `909N` (9091, 9092, 9093) |
| LocalStack | `4566` |

## 3. Usar

```bash
task escrever               # escreve na célula 1; imprime pk=cli-... indice=N
task escrever CELL=2        # escreve na célula 2 (se não for a líder, é encaminhada)
task buscar                 # scan via DR em cada célula, sem _dr_v
task ler PK=cli-...         # get-item via DR em cada célula, sem _dr_v
task inspecionar            # scan direto no LocalStack, com _dr_v
task wal CELL=1             # arquivos do WAL do Raft e do log de replicação
task logs                   # logs de todas as containers
```

Logo depois do `escrever`, o `inspecionar` pode mostrar o item em só uma ou duas tabelas: só a líder aplica antes de responder, e as followers aplicam logo em seguida (até 2 s). O `buscar` e o `ler` sempre mostram o item, porque passam pela barreira de leitura forte.

## 4. Teste automático

```bash
task e2e      # subir, escrever, buscar, ler e inspecionar; termina com "e2e: OK"
task testar   # go test -race ./... dentro do container de teste
```

## 5. Cenários de falha

Primeiro veja qual célula é a líder com `task status`. Nos exemplos abaixo a líder é a `cell-1`; troque os números se a sua for outra.

Os comandos `aws` diretos usam as credenciais de teste. Exporte-as uma vez no terminal:

```bash
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=sa-east-1 AWS_MAX_ATTEMPTS=1
```

### Escrita numa follower (CA-04)

**O que testa:** uma follower não grava sozinha. Ela encaminha a escrita inteira à líder, num único salto, e devolve ao cliente o mesmo resultado que a líder devolveria.

```bash
task escrever CELL=2
docker compose logs cell-2 | grep encaminhada
```

**Resultado esperado:**
- O `escrever` sai com 0 e imprime `pk=cli-… indice=N`, como numa escrita feita direto na líder.
- O log da `cell-2` tem a linha `escrita encaminhada à líder` com `"lider":"1"`.
- `task buscar` mostra a nova `pk` nas três células, e `task inspecionar` mostra o mesmo `_dr_v` = N nas três tabelas.

### Reinício de follower (CA-08)

**O que testa:** uma follower que cai e volta recupera do próprio disco (WAL do Raft e progresso do applier) e alcança a líder. O replay é idempotente: reaplicar não muda a versão dos itens.

```bash
task escrever
docker compose restart cell-2
task status
task inspecionar
```

**Resultado esperado:**
- A `cell-2` volta ao `/readyz` em poucos segundos.
- No `task status`, a `cell-2` mostra os mesmos `commit`, `gate` e `aplicado` da líder.
- No `task inspecionar`, o `_dr_v` de cada item na `cadastro_cliente_cell_2` é o mesmo de antes do reinício e igual ao das outras tabelas.
- Uma escrita feita enquanto a follower estava fora também aparece na tabela dela depois que ela volta.

### Queda da líder (CA-09)

**O que testa:** sem a líder, as duas células restantes ainda formam quórum (2 de 3), elegem uma nova líder e continuam aceitando escritas. A antiga líder, ao voltar, entra como follower e recebe o que perdeu.

```bash
docker compose stop cell-1
task status
task escrever CELL=2
docker compose start cell-1
task status
```

**Resultado esperado:**
- Em até 5 s (nos testes, cerca de 1,6 s) outra célula aparece como `"papel":"lider"`, com `termo` maior que antes. A `cell-1` aparece como fora do ar.
- O `escrever` funciona e imprime um índice maior que os anteriores. O salto de um índice a mais é normal: é a entrada vazia que a nova líder grava ao assumir.
- Depois do `start`, a `cell-1` volta como `follower`, com os mesmos `commit`, `gate` e `aplicado` das outras, e a escrita feita durante a queda aparece na tabela dela.

### Sem quórum (CA-07)

**O que testa:** sem 2 de 3 células, nada é confirmado nem aplicado. É a garantia central: o cliente nunca recebe 200 para uma escrita que não está durável na maioria.

```bash
docker compose stop cell-2 cell-3
sleep 3
task status
task escrever CELL=1
task status
task inspecionar
docker compose start cell-2 cell-3
task escrever CELL=1
```

**Resultado esperado:**
- Depois dos 3 s, a `cell-1` aparece como `follower` com `"lider":0`: sem contato com a maioria, ela abre mão da liderança (`CheckQuorum`).
- O `escrever` falha em cerca de 1 a 2 s com `ServiceUnavailable … NO_LEADER: no leader known, retry` (HTTP 503) e sai com código diferente de zero.
- O `commit` e o `gate` da `cell-1` ficam com o mesmo valor de antes da tentativa.
- No `task inspecionar`, nenhuma das três tabelas tem o item; a contagem de itens não muda.
- Depois do `start`, as três células voltam ao `/readyz`, uma nova líder é eleita e o `escrever` volta a responder com `pk` e índice.

Se a escrita for enviada **antes** de a liderança cair (sem o `sleep 3`), a resposta pode ser 503 `PENDING`, ou seja, resultado desconhecido. A entrada pode ter entrado no log da líder isolada e ser commitada e aplicada quando o quórum voltar. Isso é esperado: `PENDING` não significa "falhou".

### Recusas na borda (CA-10 e CA-13)

**O que testa:** tudo o que o DynamoDB recusaria é recusado **antes** de chegar ao Raft. Uma requisição inválida nunca vira entrada no log.

```bash
aws dynamodb put-item --endpoint-url http://localhost:8002 --table-name cadastro_cliente_cell_2 --item '{"pk":{"S":"x"},"_dr_v":{"N":"1"}}'
aws dynamodb put-item --endpoint-url http://localhost:8002 --table-name cadastro_cliente_cell_2 --item '{"nome":{"S":"x"}}'
aws dynamodb put-item --endpoint-url http://localhost:8002 --table-name cadastro_cliente_cell_2 --item '{"pk":{"N":"1"}}'
aws dynamodb put-item --endpoint-url http://localhost:8002 --table-name cadastro_cliente_cell_1 --item '{"pk":{"S":"x"}}'
aws dynamodb delete-item --endpoint-url http://localhost:8002 --table-name cadastro_cliente_cell_2 --key '{"pk":{"S":"x"}}'
```

**Resultado esperado:** cada comando falha com HTTP 400 e esta mensagem:

| Comando | Erro esperado |
|---|---|
| Item com `_dr_v` | `ValidationException`: `o atributo _dr_v é reservado` |
| Item sem `pk` | `ValidationException`: `One or more parameter values were invalid: Missing the key pk in the item` |
| `pk` numérica | `ValidationException`: `One or more parameter values were invalid: Type mismatch for key pk` |
| Tabela da `cell-1` enviada à `cell-2` | `ResourceNotFoundException`: `Requested resource not found` |
| `delete-item` (também `batch-write-item`) | `ValidationException`: `operação não suportada nesta fase (fase 2)` |

O `commit` da `cell-2` no `task status` é o mesmo antes e depois dos cinco comandos.

### Membros diferentes (CA-12)

**O que testa:** a lista de membros do cluster fica gravada no WAL na primeira partida. Uma célula nunca sobe com uma configuração diferente da gravada, porque isso poderia criar dois clusters incompatíveis.

Mude `PEERS` no bloco `x-dr-env` do `docker-compose.yml` (por exemplo, acrescente `,4=cell-4:7000`) e rode `docker compose up -d`.

**Resultado esperado:**
- As células com volumes antigos saem com código 1 logo na partida. Por causa do `restart: on-failure`, o Docker tenta subir de novo e falha do mesmo jeito: `docker compose ps -a` mostra `Restarting` ou `Exited (1)`, e o `/readyz` nunca responde.
- O log (`task logs`) mostra: `PEERS/LEARNERS diferem do que está gravado no WAL (gravado: votantes [1 2 3], learners []; configuração: votantes [1 2 3 4], learners []); rode task limpar`.
- Desfazendo a mudança e rodando `docker compose up -d` de novo, as células voltam normalmente com os dados que tinham.

## 6. Parar e limpar

```bash
task parar    # docker compose down, mantém os volumes do DR
task limpar   # docker compose down -v, apaga os volumes
```

**Atenção:** o LocalStack guarda os dados só em memória. Depois de `task parar` e `task subir`, os volumes do DR voltam com o log e o progresso do applier, mas as tabelas voltam vazias e o applier não reaplica o que já estava aplicado. O `/status` mostra tudo aplicado e a busca não devolve nada. Para recomeçar com dados coerentes, use `task limpar` antes de `task subir`.

## 7. Problemas comuns

| Sintoma | Causa | O que fazer |
|---|---|---|
| `docker-credential-desktop: executable file not found` | Binários do Docker Desktop fora do PATH | Veja a seção 1 |
| `subir` falha com "não ficou pronta em 60 s" | LocalStack lento para subir ou tabela ausente | `task logs`; procure "tabela conferida" e "TABLE_WAIT" |
| Célula sai com "PEERS/LEARNERS diferem do que está gravado no WAL" | Configuração mudou com volumes antigos | `task limpar` |
| `escrever` responde `NO_LEADER` | Eleição em andamento ou sem quórum | Espere 1 a 2 s e confira `task status` |
| `/readyz` responde "applier parado" | Item recusado pelo banco depois do commit (entrada venenosa) | Veja `erroApply` no `task status`; a saída é humana (ADR-038) |
| Tabelas vazias depois de `parar` e `subir` | LocalStack sem persistência | Seção 6: `task limpar` e `task subir` |

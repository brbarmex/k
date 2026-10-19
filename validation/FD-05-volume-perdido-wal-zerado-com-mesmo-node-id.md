# FD-05 · Volume perdido ou trocado: WAL zerado com o mesmo NODE_ID

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha (complemento) |
| Origem | Complemento; é a lacuna 2 conhecida do projeto |
| Requisitos | RPO = 0 (principal), FT |
| Suporte na POC hoje | **Não. Inseguro por desenho.** `openStorage` cria um WAL novo (índice 1, termo 1, sem voto) quando o diretório não existe, e o processo sobe com o **mesmo NODE_ID** fixo da configuração. O correto (ADR-034) é identidade nova e troca de membros (ConfChangeV2), que a POC não tem. |
| Injeção | numa follower: parar o pod, apagar o PVC (ou o diretório do WAL), subir de novo com o mesmo `NODE_ID`. **Rodar só em ambiente isolado.** |
| Duração | até o desfecho |

## Análise crítica

Dois desfechos possíveis, ambos inaceitáveis no produto. O cenário existe para **provar a lacuna com evidência**, não para passar.

1. **Pânico em laço (provável, inferido do etcd raft):** a líder ainda tem, para esse par, o `Match` antigo (alto). O heartbeat leva `commit = min(Match, commit)`, maior que o último índice do WAL zerado (1). O etcd raft entra em pânico com a mensagem "tocommit(N) is out of range [lastIndex(1)]. Was the raft log corrupted, truncated, or lost?". A célula reinicia em laço. Disponibilidade: as outras duas seguem; RPO preservado nesta execução.
2. **Perda de dado confirmado (sequência construída):** se a líder reiniciou antes (o `Match` zera) a célula zerada alcança em silêncio e parece saudável. O perigo aparece com esta sequência:
   1. isolar a follower B (FR-02);
   2. escrever X: confirmada por L e pela follower A (2 de 3);
   3. zerar o WAL de A e subir com o mesmo NODE_ID;
   4. derrubar L;
   5. devolver B. B pede voto; A (log vazio) vota em B, porque o log de B é "mais completo" que o vazio; B vira líder **sem X**.
   6. X, que o cliente recebeu como 200, some.

   Esperado: o oráculo acusa `dr_val_confirmadas_ausentes_total > 0`. É a prova objetiva do RPO > 0 que a lacuna causa.

## Critérios (esperados como FALHA hoje)

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Desfecho 1 observado | log de pânico; `kube_pod_container_status_restarts_total` subindo |
| A2 | Desfecho 2 reproduzido | ORC-04 R1 com X ausente |
| A3 | Detectabilidade | M-16: a identidade do WAL muda e o NODE_ID não; um alerta sobre isso teria apontado o risco antes do passo 4 |
| A4 | Evidência indireta | campo `ultimo` do log `partida: WAL relido` (M-05) ausente ou 1, com `dr_peer_match_index` alto na líder |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK (só para caracterizar a carga do passo 2).
- Golden signals: G-ERR-1..5, G-TRF-2.
- Além: B-RAFT-1, B-RAFT-3, B-RAFT-4 (`vote` concedido pela célula zerada), B-INV-2, B-K8S, M-05, M-16, ORC-05.

## Mudança esperada no Incremento B

Com o fail-fast do volume vazio (DC-12, RT-21, CA-28 da trecho da compactação), a célula com o volume zerado **não sobe** num grupo que já existe: os dois desfechos acima deixam de acontecer, e o resultado esperado passa a ser "recusa com a mensagem que aponta para `parklot/adr-troca-de-pvc-em-cell-ativa.md`; as outras duas seguem". A devolução segura da célula é a feature `troca-de-pvc-em-cell-ativa`.

## Decisão que este cenário alimenta

Implementar na POC a regra do ADR-034 (identidade nova e ConfChange), ou declarar na apresentação que a POC não cobre troca de disco.

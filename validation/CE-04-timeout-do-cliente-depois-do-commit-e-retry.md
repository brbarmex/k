# CE-04 · Timeout do cliente depois do commit e nova tentativa

| Campo | Valor |
|---|---|
| Categoria | Caminho eventual (complemento) |
| Origem | Complemento (cenário C07 do pacote de arquitetura) |
| Requisitos | RPO = 0 do ponto de vista do cliente (o que ele acha que perdeu) |
| Suporte na POC hoje | **Parcial.** A POC devolve PENDING (500) quando o prazo estoura depois de propor, e o PutItem é idempotente pelo conteúdo. **Não há dedupe de retry na POC** (o dedupe com janela de 5 min é do produto): cada nova tentativa vira uma entrada nova no log, com versão maior. |
| Injeção | atrasar o apply da líder (pausar o DynamoDB dela, ver FB-02) ou `aws:eks:pod-network-latency` entre as células, para estourar o prazo de 7,5 s; cliente com SDK real (timeout 30 s × 3 tentativas) |
| Duração | 10 min |

## Objetivo

Provar que uma escrita que voltou PENDING e foi repetida termina **uma** vez no estado final, e medir o que a falta de dedupe custa.

## Análise crítica

- Sem dedupe, duas tentativas do mesmo PutItem viram duas entradas. Com pk única por escrita (CRG), o efeito é só custo (2× WCU e log). Com chave compartilhada, aparece o problema real: a tentativa antiga, reaplicada depois de uma escrita mais nova de outro cliente na mesma pk, **sobrescreve** a mais nova. Isso não é perda de dado confirmado para o sistema, mas é perda para quem escreveu por último. O cenário precisa de uma variante com chave compartilhada (CRG-4) para mostrar isso.
- O oráculo trata a tentativa como uma operação lógica: o identificador lógico do pedido vai no ledger.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Toda operação lógica com algum 200 está nas 3 tabelas | ORC-04 R1 agrupado por operação lógica |
| A2 | Número de entradas duplicadas medido | entradas no log (`dr_commit_index`) − operações lógicas confirmadas |
| A3 | Variante de chave compartilhada: sobrescrita por retry atrasado medida | ORC-06 aponta as leituras que viram valor "voltando no tempo" |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por tentativa e por operação lógica (soma das tentativas), L-ETAPAS.
- Golden signals: G-ERR-1 (`pending`), G-ERR-5 (retries do SDK), G-TRF-2.
- Além: B-CUSTO (WCU por operação lógica), B-IDX.

## Lacunas

Dedupe na POC (fora do escopo de métrica: é funcionalidade); M-09.

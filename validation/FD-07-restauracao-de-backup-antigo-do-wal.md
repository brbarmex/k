# FD-07 · Restauração de um backup antigo do WAL numa célula de grupo ativo

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha de operação (complemento) |
| Origem | Complemento: risco registrado na P-04 e em `parklot/adr-troca-de-pvc-em-cell-ativa.md` |
| Requisitos | RPO = 0 (é o cenário que o viola), FT |
| Suporte na POC hoje | **Não, por desenho.** Backup e restauração do banco e do WAL são de outro escopo (P-04). Um WAL antigo tem estado, então a célula sobe normalmente, mas esqueceu os votos e as entradas confirmadas depois do backup. O fail-fast do volume vazio (DC-12, Incremento B) não pega esse caso. |
| Injeção | snapshot EBS do volume de uma follower; 10 min de CRG-1; parar a follower; restaurar o volume a partir do snapshot EBS; subir com o mesmo `NODE_ID`. **Só em ambiente isolado.** |
| Duração | até o desfecho |

## Análise crítica

- É o mesmo perigo do FD-05 (volume perdido), mas sem nenhuma pista para o app: o WAL é íntegro, só é velho. A sequência de perda é a mesma do FD-05, passos 1 a 6, trocando "zerar o WAL de A" por "restaurar um backup antigo do WAL de A".
- A evidência indireta é a mesma: o último índice do WAL na partida (log `partida: WAL relido`, campo `ultimo`) menor que o `dr_peer_match_index` que a líder tinha para a célula.
- O cenário existe para provar ao outro escopo (backup) a regra: **um backup do WAL só serve para recriar o grupo inteiro, com `epoch` nova**, nunca para devolver uma célula a um grupo vivo.

## Critérios (esperados como FALHA de segurança)

| # | Critério | Como se comprova |
|---|---|---|
| A1 | A célula sobe sem aviso | `dr_ready == 1`; nenhum log de recusa |
| A2 | Evidência indireta | `ultimo` da partida < `dr_peer_match_index{par=<célula>}` anterior na líder |
| A3 | Perda reproduzida na sequência do FD-05 | ORC-04 R1 com item confirmado ausente |

## Métricas

- Além: B-RAFT-3, B-RAFT-4 (`vote` concedido pela célula restaurada), logs da partida, ORC-05.

## Decisão que este cenário alimenta

Uma checagem na partida (ex.: comparar o `HardState.term` ou o último índice com o que os pares informam) poderia recusar um WAL velho. Fica para a feature `troca-de-pvc-em-cell-ativa`.

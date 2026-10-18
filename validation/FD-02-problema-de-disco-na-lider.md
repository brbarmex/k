# FD-02 · [disco] Problema de disco na célula líder (comando ou desanexar)

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha |
| Origem | Lista do Bruno |
| Requisitos | RPO = 0, FT, HA |
| Suporte na POC hoje | **Parcial**, pelas mesmas razões de FD-01, e com um problema extra na variante de pausa (abaixo). |
| Injeção | variantes a, b e c de FD-01, aplicadas ao volume da líder |
| Duração | 10 min antes, falha, até a volta + 10 min |

## Análise crítica

- **Variante a (pausa de I/O) é a mais perigosa para a disponibilidade.** Com o fsync preso, o laço do Raft da líder para: sem tick e sem heartbeat. As followers elegem nova líder após o timeout. A líder antiga continua viva e pronta: `/livez` e `/readyz` respondem 200, e o Kubernetes não a tira do Service. Comportamento esperado (inferido do código e do etcd raft, a confirmar):
  - o motor do Raft dela ainda recebe mensagens e, ao ver o termo da nova líder, vira follower. O `/status` e as métricas (`dr_is_leader`) passam a mostrar "follower";
  - mas o caminho de escrita usa a líder guardada pelo laço preso, que ainda diz "eu". Antes de ela ver o termo novo, a escrita é proposta e espera até o prazo (PENDING em 7,5 s); depois, o Raft descarta a proposta e a resposta vira 400 `ThrottlingException` com `causa="nao_lider"`, **em vez de encaminhar à nova líder**;
  - leituras fortes por ela voltam `read_timeout`.

  Ou seja: quem chega pela API da líder antiga fica sem escrita **durante toda a pausa**, e o painel mostra "follower" enquanto o caminho de escrita ainda age como líder. O Delta-T para essa entrada é a duração da pausa, não o tempo de eleição.
- Na volta da I/O, a líder antiga descobre o termo novo e passa a follower. As escritas que ela mandou às followers **antes** do fsync (MsgApp sai antes do fsync na líder, ADR-056) podem ter sido confirmadas pela nova líder; nesse caso a líder antiga devolve 200 para elas ao ver o commit. Isso é correto, e só o oráculo distingue de um ACK falso.
- Variantes b e c: a líder cai na hora (fatal) e o cenário vira FP-03 variante b, com a questão de identidade de FD-01.

## Passos

Iguais a FD-01, com a líder como alvo. Na variante a, rodar com pausa de 5 s (acima do timeout de eleição), 30 s e 120 s.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 e nenhum ACK falso | ORC-04 |
| A2 | Uma líder por termo | B-INV-2 |
| A3 | Delta-T pelas followers | RTO-02..05 ≈ FP-03 |
| A4 | Delta-T pela API da líder antiga (variante a) | ledger por célula de entrada; esperado ≈ duração da pausa (**gap de HA** a registrar); separar PENDING (antes de ela ver o termo novo) de `nao_lider` (depois) |
| A5 | Líder travada é detectável durante o travamento | M-15; sem ele: **não comprovado** |
| A6 | Variantes b e c | como FD-01 A5 e A6 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por célula de entrada (a da líder antiga dá o max da pausa); L-FSYNC, L-LOOP, L-TICK da líder antiga; L-ACK-S e L-APPLY da nova líder.
- Golden signals: G-TRF-1..3, G-ERR-1 (`pending` na antiga), G-ERR-2, G-ERR-5, G-SAT-1 (requisições presas na antiga), G-SAT-6.
- Além: B-RAFT-1, B-RAFT-5 (as followers deixam de ver heartbeat), B-INV-1..3, `kube_pod_status_ready` da antiga (deve continuar 1: é a evidência do problema), M-05, M-14, M-15.

## Lacunas

M-14, M-15. Proposta a decidir (fora da POC hoje): `/readyz` falhar quando o último tick tem mais de 1 timeout de eleição.

# CE-01 · Líder recebendo tráfego e troca de liderança abrupta por comando

| Campo | Valor |
|---|---|
| Categoria | Caminho eventual |
| Origem | Lista do Bruno |
| Requisitos | RPO = 0, HA (tempo sem líder) |
| Suporte na POC hoje | **Não.** Não existe comando de troca de liderança: `TransferLeadership` saiu da POC em 17/10 (commit `9710fc3`), e o encerramento do pod (SIGTERM) não passa a liderança antes de parar. A transferência planejada é do produto (ADR-013). |
| Injeção | depende da variante (abaixo) |
| Duração | 10 min de CF-03 antes, a troca, 10 min depois |

## Análise crítica: o cenário, como escrito, está mal definido

"Comando de troca de liderança abrupto" mistura duas coisas opostas:

1. **Troca planejada** (alguém manda a líder passar a vez): o objetivo é janela quase zero, porque a líder para de aceitar propostas, espera a sucessora alcançar o log e só então passa. Não é abrupta por natureza.
2. **Troca abrupta** (a líder some sem aviso): é uma falha, coberta por FP-03, FP-06, CE-02 e FR-01.

Para a validação, o cenário foi separado em variantes com comportamento esperado distinto. A variante **a** é a que o Bruno descreveu e não tem suporte; as outras rodam hoje.

| Variante | Como se provoca | Suporte | O que se espera |
|---|---|---|---|
| a. Transferência planejada por comando | endpoint de administração (inexistente) que chama `TransferLeadership` no etcd raft | **Não** | escritas em voo terminam ou recebem recusa segura; Delta-T ≈ 1 RTT + 1 fsync; zero INCERTA |
| b. Transferência disparada no meio de uma rajada | igual a "a", com CRG-2 | **Não** | idem, com contrapressão `nao_lider` em vez de PENDING |
| c. Encerramento gracioso da líder (SIGTERM, `kubectl delete pod`) | `aws:eks:pod-delete` | Sim | sem transferência: Delta-T ≈ timeout de eleição (1 a 2 s com `ELECTION_TICKS=10`); escritas em voo viram PENDING |
| d. Morte abrupta (SIGKILL) | `kubectl delete pod --grace-period=0 --force` ou `kill -9` no container | Sim | igual a "c"; diferença mede o que o encerramento gracioso economiza (hoje, provavelmente nada) |

A comparação **c × a** é o argumento para trazer o ADR-013 para a POC: se o Delta-T de "c" for aceitável, a transferência planejada não é necessária para a validação.

## Pré-condições

Baseline de CF-01 e CF-03. Para a e b: especificar e implementar o comando antes (fora deste documento).

## Passos (variantes c e d)

1. CF-03 com CRG-1 por 10 min.
2. T0: encerrar a líder (c ou d), com anotação no Grafana.
3. Manter a carga por 10 min; a célula volta sozinha (o StatefulSet recria o pod).
4. Quiescência, oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 zerado; INCERTAS apenas no intervalo [T0, T_eleição + 7,5 s] |
| A2 | Nunca duas líderes no mesmo termo | B-INV-2 (M-14) |
| A3 | Delta-T medido | RTO-02 a RTO-08 registrados; hipótese: RTO-04 ≤ 3 s em c e d |
| A4 | A líder antiga volta como follower e alcança | RTO-07; `dr_is_leader == 0` na célula antiga |
| A5 | Variante a (quando existir) | RTO-08 ≤ 1 s e zero INCERTA |

## Métricas

### Latência (p90, p99, p99.9, max), em três janelas: antes, [T0, T_estável], depois

| Sinal | Observação |
|---|---|
| L-ACK por célula de entrada | o max da janela de falha mostra quem esperou a eleição inteira |
| L-HTTP | inclui 503 e 500 |
| L-FWD (M-02) | encaminhamentos presos na líder morta |
| L-TICK, L-LOOP na nova líder | |
| L-APPLY na nova líder | o atraso dela entra no RTO (CE-05) |

### Golden signals

- Tráfego: G-TRF-1..3; queda de G-TRF-2 a zero define a janela.
- Erros: G-ERR-1 (`no_leader`, `pending`), G-ERR-2 (503, 500), G-ERR-3 (`nao_lider`), G-ERR-5 (cliente).
- Saturação: G-SAT-1 (requisições presas esperando líder), G-SAT-2 na nova líder.

### Além dos golden signals

B-RAFT-1 (termo e líder), B-RAFT-4 (`vote`, `prevote`), B-RAFT-5, B-INV-1..3, B-K8S (reinício, tempo até pronto), RTO-01..08.

## Prova de RPO = 0

ORC-04. As INCERTAS da janela devem existir nas 3 tabelas ou em nenhuma; contar quantas de cada.

## Lacunas

M-02, M-09, M-13, M-14; na variante a, todo o comando.

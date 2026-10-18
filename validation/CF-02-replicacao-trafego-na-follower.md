# CF-02 · Replicação entre as células com tráfego chegando numa follower

| Campo | Valor |
|---|---|
| Categoria | Caminho feliz |
| Origem | Lista do Bruno |
| Requisitos | RPO = 0, HA (qualquer célula aceita a escrita) |
| Suporte na POC hoje | **Sim.** A follower encaminha a escrita inteira à líder num salto (`peerclient.Forward`, ADR-010); a líder responde. |
| Injeção | nenhuma |
| Duração | igual a CF-01, com CRG-1 e CRG-2 enviados só a uma follower; depois às duas followers |

## Objetivo

Provar que a escrita recebida pela follower tem o mesmo resultado de RPO que a recebida pela líder, e medir o custo do salto extra.

## Análise crítica

- O caminho tem **dois prazos**: `FORWARD_TIMEOUT` (3 s) na follower e `REQUEST_DEADLINE` (7,5 s) na líder. Se o encaminhamento estoura os 3 s, a follower devolve PENDING (500) mesmo que a líder ainda confirme a escrita depois: o cliente vê INCERTA de algo que vai existir. O oráculo precisa aceitar isso (R3), e o cenário deve contar quantas vezes acontece.
- Hoje não há métrica de latência nem de resultado do encaminhamento: `dr_forwards_total` só conta tentativas. Sem M-02, a diferença entre CF-02 e CF-01 só aparece no cliente.
- Cada escrita encaminhada grava um log em nível Info ("escrita encaminhada à líder"). Em CRG-2 isso pode virar milhares de linhas por segundo: medir CPU e volume de log da follower.
- Uma escrita que chega já encaminhada a uma célula que não é mais a líder não é reencaminhada (`r.Forwarded` → `ErrNoLeader`, 503). No caminho feliz isso não deve acontecer; se acontecer, há troca de líder escondida.

## Pré-condições

As de CF-01, mais o baseline de CF-01 já medido.

## Passos

1. Identificar líder e followers.
2. Rodar CRG-1 contra **uma** follower; quiescência; oráculo.
3. Rodar CRG-1 dividido entre **as duas** followers; quiescência; oráculo.
4. Repetir 2 e 3 com CRG-2.

## Resultado esperado

Mesmo resultado de RPO que CF-01. L-ACK maior que em CF-01 por cerca de um RTT entre células mais a serialização do gRPC.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 zerado |
| A2 | Todo tráfego da follower foi encaminhado | `rate(dr_forwards_total{celula=follower})` ≈ G-TRF-1 da follower; `dr_writes_total{resultado="ok"}` cresce **só** na líder |
| A3 | Sem eleição | como CF-01 A2 |
| A4 | Custo do salto | p99 de L-ACK(CF-02) − p99 de L-ACK(CF-01) ≤ 2 × RTT p99 entre células + 5 ms (hipótese) |
| A5 | Zero INCERTA | contagem de 500 = 0; se > 0, cruzar com L-FWD |
| A6 | Log não satura | CPU da follower em CRG-2 ≤ CPU da líder em CF-01 + 20% (hipótese) |

## Métricas

### Latência (p90, p99, p99.9, max)

| Sinal | Observação |
|---|---|
| L-ACK por célula de entrada | comparar com CF-01 em cada percentil e no max |
| L-FWD (M-02) | sem ele: L-HTTP da follower − L-ACK-S da líder como aproximação |
| L-HTTP na follower e na líder | |
| L-FSYNC, L-LOOP, L-TICK | devem ser iguais a CF-01 |
| L-APPLY por célula | igual a CF-01 |

### Golden signals

- Tráfego: G-TRF-1 (entrada nas followers), G-TRF-2 (ACK só na líder), G-TRF-3 (encaminhamentos).
- Erros: G-ERR-1 a G-ERR-5; atenção a 503 com `x-dr-code` e 500.
- Saturação: G-SAT-1 na follower (requisições presas no encaminhamento) e na líder; G-SAT-4 (CPU por causa do log).

### Além dos golden signals

B-RAFT-1, B-RAFT-3, B-INV-1, B-REDE (bytes do Forward em gRPC), M-10 (latência por método gRPC), volume de log por célula.

## Prova de RPO = 0

ORC-04, com a célula de entrada no ledger para separar o resultado por caminho.

## Lacunas de observabilidade

M-02 e M-10 são o que separa o custo do encaminhamento do resto; M-09 para ver a latência das escritas que não deram 200.

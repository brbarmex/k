# FR-01 · Partição de rede isolando a líder, com tráfego chegando nela

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha de rede (complemento) |
| Origem | Complemento: é o cenário clássico de split brain e não está na lista |
| Requisitos | RPO = 0 (a isolada nunca confirma), FT, HA |
| Suporte na POC hoje | **Sim.** Com `CheckQuorum`, a líder isolada deixa de ser líder após um timeout de eleição sem ouvir a maioria; enquanto isso, ela não consegue confirmar nada (precisa de 2 de 3). |
| Injeção | `aws:eks:pod-network-blackhole-port` na porta gRPC dos pares (7000) na líder, entrada e saída; ou regra de security group/NACL entre a subnet da líder e as outras. A API (8000) da líder continua acessível ao gerador. |
| Duração | 10 min antes, 2 min isolada, 10 min depois |

## Análise crítica

- O cenário prova a propriedade mais importante para RPO = 0 com HA: uma líder que perdeu a maioria **não confirma**, mesmo recebendo tráfego e mesmo achando por um tempo que ainda é líder.
- O Prometheus precisa alcançar a líder isolada (AMB-10): a partição é só no gRPC entre células, não na rede de observabilidade. Se o isolamento for por subnet inteira, a coleta também cai e o cenário perde a evidência do lado isolado.
- Durante o isolamento, as requisições que chegam à isolada: antes da renúncia, propostas esperam e viram PENDING; depois, a isolada fica sem líder e responde 503 após `WAIT_LEADER`. Encaminhar à nova líder não é possível (a rede entre elas está cortada).
- As duas outras elegem nova líder e seguem: o tráfego que chega a elas tem o Delta-T de FP-03.
- Ao fim, a cauda da isolada é descartada (CE-06).

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Nenhum 200 da isolada depois do isolamento | ledger por célula de entrada (a única exceção é uma escrita já confirmada antes do corte e respondida logo depois) |
| A2 | RPO = 0 | ORC-04; INCERTAS da isolada ausentes das 3 tabelas |
| A3 | Uma líder por termo | B-INV-2; duas células com `dr_is_leader == 1` em termos diferentes é esperado por até 1 timeout de eleição |
| A4 | Renúncia da isolada | `dr_leader_id == 0` nela em ≤ 2 × timeout de eleição |
| A5 | Delta-T no lado da maioria | RTO-02..05 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por célula de entrada; L-HTTP das respostas 503 da isolada (~`WAIT_LEADER`).
- Golden signals: G-TRF-1..3, G-ERR-1..5, G-SAT-1 da isolada.
- Além: B-RAFT-1, B-RAFT-2 (`dr_quorum_ok` vai a 0 na isolada), B-RAFT-4, B-RAFT-5, `dr_peer_send_dropped_total`, B-INV-1..3, M-10, M-14.

## Lacunas

M-10, M-14.

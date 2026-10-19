# FR-02 · Partição de rede isolando uma follower

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha de rede (complemento) |
| Origem | Complemento |
| Requisitos | FT, HA, RPO = 0 |
| Suporte na POC hoje | **Sim.** `PreVote` impede que a follower isolada, ao voltar, derrube a líder com um termo inflado. |
| Injeção | igual a FR-01, numa follower; 30 s e 5 min |
| Duração | 10 min antes, isolamento, 10 min depois |

## Análise crítica

- Sem `PreVote`, a isolada incrementaria o termo a cada tentativa e, ao voltar, forçaria uma eleição desnecessária. A POC liga `PreVote`; o cenário prova que a volta **não** muda o termo.
- Com o isolamento de 5 min e CRG-2, a líder pode passar do teto de RAM (`RAFT_RAM_MAX`) e a isolada volta atrás da cauda (FL-02): no Incremento A ela fica parada em `raft.atrasCauda`. Com 30 s, não.
- Escritas que chegam à isolada não conseguem ser encaminhadas: a isolada fica sem líder e responde 503 após `WAIT_LEADER`. Leituras também.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04 |
| A2 | Termo não muda na volta | `dr_raft_term` da líder constante |
| A3 | Sem impacto nas outras entradas | p99 de L-ACK ≤ 1,2 × baseline |
| A4 | Alcance após a volta | `dr_peer_lag` → 0, `dr_apply_lag` → 0 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por célula de entrada; L-APPLY da isolada na volta.
- Golden signals: G-TRF-1, G-ERR-1, G-ERR-2, G-ERR-4, G-ERR-5.
- Além: B-RAFT-1, B-RAFT-3, B-RAFT-4 (`prevote` enviado pela isolada, sem `vote`), B-DRENO.

## Lacunas

M-10.

# FP-03 · [pod-restart] Queda da célula líder

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha |
| Origem | Lista do Bruno |
| Requisitos | RPO = 0, FT, HA (Delta-T da eleição) |
| Suporte na POC hoje | **Sim.** As duas followers elegem nova líder; a antiga volta como follower. Teste local equivalente: `task test-leader-failover`, `task test-leader-outage-load`. |
| Injeção | variantes a/b/c de FP-01, aplicadas à líder |
| Duração | 10 min antes, falha, 10 min depois |

## Análise crítica

- É o cenário que mede o Delta-T principal. Ele é a soma de: detecção (timeout de eleição aleatório entre 1 e 2 s com `ELECTION_TICKS=10`), pré-voto e voto (RTTs + fsync do HardState), descoberta da nova líder pelas células que recebem tráfego, nova tentativa do cliente (SDK) e, se a nova líder estiver atrasada no apply, a admissão (CE-05). O critério só é útil com essa decomposição.
- Na variante **a** (SIGTERM), o app não passa a liderança antes de parar (não há ADR-013 na POC): o encerramento gracioso não ajuda a disponibilidade e pode até piorar se a API parar de aceitar antes do Raft parar de mandar heartbeat. Medir a diferença entre a e b.
- Escritas em voo na líder morta viram PENDING (no prazo de 7,5 s) ou erro de conexão. Escritas encaminhadas pelas followers à líder morta esperam até `FORWARD_TIMEOUT` (3 s) e viram PENDING: são INCERTAS que podem ou não ter sido confirmadas, dependendo de o MsgApp ter chegado à outra follower antes da queda.
- O tráfego que vai direto à API da líder caída é perdido até o roteamento mudar: em produção depende do roteador; aqui, registrar.

## Passos

1. CF-03 com CRG-1 (repetir com CRG-2).
2. T0: derrubar a líder.
3. 10 min de carga; a antiga volta sozinha.
4. Quiescência; oráculo.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RPO = 0 | ORC-04; INCERTAS só na janela [T0 − 7,5 s, T_eleição + 7,5 s] |
| A2 | Uma líder por termo | B-INV-2 |
| A3 | Delta-T decomposto | RTO-02..08; hipótese de aceite: RTO-04 p50 de 10 execuções ≤ 3 s, max ≤ 5 s |
| A4 | Antiga volta como follower e alcança | RTO-07; B-INV-1 nela |
| A5 | Repetibilidade | 10 execuções por variante; publicar distribuição de RTO-04 (p50, p90, max) |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK por célula de entrada em três janelas; L-FWD (M-02); L-APPLY e L-ACK-S da nova líder.
- Golden signals: G-TRF-1..3, G-ERR-1..5, G-SAT-1, G-SAT-2 da nova líder.
- Além: B-RAFT-1, B-RAFT-4 (`prevote`, `vote`), B-RAFT-5, B-INV-1..3, B-K8S, M-05, RTO-01..08.

## Lacunas

M-02, M-05, M-09, M-13, M-14.

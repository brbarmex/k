# CE-05 · Follower com atraso grande de apply vira líder

| Campo | Valor |
|---|---|
| Categoria | Caminho eventual (complemento) |
| Origem | Complemento (risco 42 do pacote: atraso do apply entra no RTO) |
| Requisitos | HA (o RTO real) |
| Suporte na POC hoje | **Sim, com efeito colateral conhecido.** A nova líder só confirma depois do apply local, e a admissão recusa com `atraso_apply` enquanto `gate − aplicado > APPLY_LAG_MAX`. |
| Injeção | deixar uma follower com apply atrasado (pausar o DynamoDB dela com política IAM de negação temporária não serve, pois vira erro permanente; usar CPU estrangulada no pod ou throttling de WCU provisionado baixo na tabela dela) e então derrubar a líder |
| Duração | 10 min de atraso crescente, falha da líder, 15 min depois |

## Objetivo

Medir o Delta-T real quando a sucessora tem o banco atrasado: a eleição é rápida, mas a escrita só volta quando o apply dela alcança.

## Análise crítica

- O Raft elege qualquer célula com o log mais completo; ele não sabe nada sobre o banco. Quem ganha pode ter milhares de entradas sem aplicar.
- Este é provavelmente o maior componente do RTO em produção, maior que a eleição. Nenhum cenário da lista original o mede.
- Para forçar a escolha da célula atrasada como sucessora, a outra follower precisa estar fora (FP-10) ou o teste repete até cair nela.

## Critérios de aceite

| # | Critério | Como se comprova |
|---|---|---|
| A1 | RTO-05 (T_admissão) medido e decomposto | `dr_apply_lag` da nova líder no instante da eleição ÷ vazão do apply dela ≈ T_admissão |
| A2 | Durante T_admissão só há recusa segura | 400 `ThrottlingException` com `causa="atraso_apply"`; 500 = 0 |
| A3 | RPO = 0 | ORC-04 |

## Métricas

- Latência (p90, p99, p99.9, max): L-ACK depois da eleição, L-APPLY da nova líder, L-REPL.
- Golden signals: G-TRF-2, G-ERR-3 (`atraso_apply`), G-SAT-2 da nova líder.
- Além: B-DRENO, RTO-03 × RTO-04 × RTO-05.

## Lacunas

M-11.

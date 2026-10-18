# FB-04 · Escrita direta no DynamoDB, sem passar pela camada (bypass)

| Campo | Valor |
|---|---|
| Categoria | Caminho de falha do banco (complemento) |
| Origem | Complemento (C20 do pacote) |
| Requisitos | RPO = 0 (a replicação só garante o que passou por ela) |
| Suporte na POC hoje | **Não.** Não há detecção nem bloqueio de escrita direta; a escrita direta só é sobrescrita se um PutItem da camada vier depois para a mesma pk (o apply condicional usa `_dr_v`: um item sem `_dr_v` é sobrescrito). |
| Injeção | `aws dynamodb put-item` direto na tabela de uma célula, com pks novas e com pks já existentes |
| Duração | curta |

## Análise crítica

- O oráculo pega a divergência (R4), o app não. Para a validação, o ponto é **mostrar** que a garantia vale só para o tráfego que passa pela camada e que um bloqueio por IAM (só o papel da camada escreve) é requisito de implantação.

## Critérios (esperados como divergência hoje)

| # | Critério | Como se comprova |
|---|---|---|
| A1 | Divergência detectada pelo oráculo | `dr_val_itens_divergentes_total > 0` |
| A2 | Nada no app acusa | nenhuma métrica do app muda |
| A3 | Com IAM restrito, a escrita direta é negada | `AccessDenied` no comando |

## Métricas

- Golden signals: nenhum do app se altera (é a evidência da lacuna).
- Além: ORC-05.

## Lacunas

Detecção de deriva (comparação periódica entre tabelas) e bloqueio por IAM.

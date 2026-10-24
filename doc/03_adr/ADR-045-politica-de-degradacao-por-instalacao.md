# ADR-045 — `DEGRADED_POLICY` obrigatória por instalação

**Status:** **Substituído pelo ADR-064** (24/10/2026); era Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

O header `x-dr-mode: degraded` ([ADR-027](ADR-027-sinalizacao-do-modo-degradado.md)) avisa, mas os SDKs o ignoram: não é aceite de risco. Como cada cliente tem a sua instalação ([ADR-043](ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md)), a escolha pode ser feita por cliente.

## Decisão

1. Variável **obrigatória, sem valor padrão**, validada na partida: `DEGRADED_POLICY=accept` ou `reject`. `DEGRADED_LEASE` (D) é obrigatória só com `accept`; com `reject` é ignorada.
2. **`accept`:** vale o modo degradado da opção B, limitado pelo lease do [ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), com o header e a métrica. Exige o aceite formal do RPO > 0 e do valor de D (PEND-02).
3. **`reject`:** a líder nunca propõe `DEGRADE`. Sem o gate da Azure a escrita não é ACKada: o cliente recebe `503 PENDING` (resultado desconhecido, [ADR-028](ADR-028-timeouts-em-cascata.md)) e o RPO é 0 sempre. A escrita já commitada na AWS espera o gate.
4. A recomendação para dados monetários que não aceitam RPO > 0 é `reject`; a escolha é de cada cliente e fica registrada na configuração e na auditoria da instalação.

## Alternativas consideradas

- Política única para todos: ignora que os clientes têm tolerâncias diferentes.
- Só o header como aviso: o SDK não o lê.

## Consequências

- Com `reject`, uma queda da Azure ou do link para as escritas do cliente (disponibilidade menor, RPO 0).
- A PEND-02 e o dimensionamento do log ([C29](../02_cenarios/atualizados/C29-disco-do-log-em-degradacao-longa.md)) só valem para instalações `accept`.
- A política é testada nos dois valores na suíte de falhas.

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

Substituído por [ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md): o comportamento com a Azure fora é **um só** (a AWS segue, RPO > 0 sinalizado). Não existe mais `reject`. Consequência a registrar: um cliente que exige RPO 0 sempre perde a opção de parar as escritas no corte do link; se algum exigir, este ADR precisa voltar.

## Onde está o detalhe

- [C32-particao-e-lease-do-modo-degradado.md](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md)
- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)

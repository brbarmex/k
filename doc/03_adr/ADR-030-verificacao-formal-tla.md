# ADR-030 — Modelo TLA+ do gate antes do código do gate

**Status:** Decidido (Bruno) · **Data:** 12/10/2026

## Contexto

O gate e o par DEGRADE/RESTORE são lógica nossa fora de uma prova conhecida.

## Decisão

Escrever e verificar um modelo TLA+ de gate, DEGRADE/RESTORE e troca de líder antes de implementar o gate.

## Alternativas consideradas

Só testes de simulação.

## Consequências

Alguns dias de trabalho antes do código do gate.

## Atualização de 13/10/2026

O modelo também deve cobrir a regra candidata "entrar em modo degradado só com 3 de 3 votantes AWS vivos" e compará-la com a regra atual. O Bruno decidiu **decidir essa regra com o resultado do modelo**, em vez de adotá-la antes ([R06](../02_cenarios/a_rever/R06-duas-azs-aws-caem.md)). A decisão final depende da PEND-01.

## Atualização de 13/10/2026 (revisão crítica)

O escopo do modelo cresce: lease do modo degradado e promoção entre nuvens ([ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md), incluindo prorrogação e partição assimétrica), troca de membros, dedupe, resultado desconhecido, invariante de DELETE e replay ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)) e a barreira da leitura forte ([ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md)). O modelo nasce junto com a decisão da fase 1 do plano, porque o lease só vale se for modelado. A PEND-01 deixou de depender só da resposta dos outros arquitetos: o desenho está no ADR-044.

## Atualização de 13/10/2026 (group commit, ADR-056)

O modelo separa **log volátil e log durável** por nó, com queda que perde o volátil em qualquer passo; `MsgAppResp`, voto e auto-ACK da líder dependem só do durável. Invariante nova: **toda entrada ACKada está no log durável de pelo menos 2 nós AWS e 2 nós Azure**. Cobre o modo síncrono com F3 (só a líder envia `MsgApp` antes de gravar) e o `AsyncStorageWrites`, antes do código do adaptador `etcdraft` ([ADR-056](ADR-056-group-commit-por-shard-e-nada-sai-antes-do-fsync.md)). O modelo do próprio `etcd-io/raft` (`tla/etcdraft.tla`) não cobre o gate nem o lease.

## Atualização de 14/10/2026 (inventário dos ADRs, resposta 8a)

"Antes do código do gate" vale para o **gate com learners reais** (fase AWS). A POC roda sem Azure, com gate = commit do Raft, e o código de domínio do gate (`domain/gate.go`, testado com learners simulados) não conta como o código do gate para esta regra. O modelo continua obrigatório antes de ligar learners reais.

## Onde está o detalhe

- [03_consenso_raft_quorum_e_gate.md](../01_arquitetura/03_consenso_raft_quorum_e_gate.md)

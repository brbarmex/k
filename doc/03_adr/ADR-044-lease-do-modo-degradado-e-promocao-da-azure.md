# ADR-044 — Modo degradado com lease: a AWS para sozinha e a Azure só promove depois de D mais margem

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026); o valor de D depende da PEND-02 e a regra precisa ser provada no TLA+ ([ADR-030](ADR-030-verificacao-formal-tla.md)) · **Data:** 13/10/2026

## Contexto

A revisão crítica apontou o risco de duas pontas aceitando escrita. O problema nasce da opção B ([ADR-014](ADR-014-modo-degradado-opcao-b.md)): com o link caído, a AWS segue escrevendo em modo degradado. Se uma pessoa promover a Azure nesse intervalo, as duas pontas aceitam escrita. Revogar credenciais ([ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md)) e checar a epoch no boot não impedem isso, porque a ponta antiga não sabe que foi substituída. Sem terceiro domínio ([ADR-008](ADR-008-terceiro-dominio.md), restrição dura de só AWS e Azure), a autoridade única só pode vir do tempo.

## Decisão

1. **A entrada `DEGRADE` marca o modo degradado e carrega uma validade (lease) de duração D** (`DEGRADED_LEASE`). D é o teto de duração do RPO > 0 que o negócio precisa aprovar (PEND-02). **O prazo conta desde o último contato com a maioria dos learners da Azure** (`t_c`: o instante de envio do último heartbeat ou `AppendEntries` que a maioria Azure confirmou), no relógio monotônico da líder, e **não** desde a proposta do `DEGRADE`. Assim a janela não depende de quando chega a primeira escrita depois da partição: se a proposta ocorrer horas depois de `t_c`, o lease já está vencido.
2. **A líder confere a validade a cada ACK**, depois do fsync e antes de responder, e não apenas em um temporizador. Pausa de processo (GC, VM congelada) é o caso perigoso. Um salto de tempo detectado conta como lease expirada. **Uma líder nova que herda o `DEGRADE` não herda `t_c`:** trata o lease como expirado até obter contato com a Azure ou um `RENEW`. O mesmo vale para o reinício do nó.
3. **Ao fim de D a AWS para de aceitar escrita sozinha** (`503 LEASE_EXPIRED`). Leitura e Raft continuam. Só uma pessoa prorroga, por uma entrada replicada `RENEW(epoch, nova validade)` no Raft da AWS.
4. **A Azure só pode ser promovida depois de D + M** contados no relógio monotônico dela a partir do último contato recebido da AWS. `M = deriva máxima de relógio + pausa máxima de processo`. Como a AWS contou D desde o último contato confirmado por uma maioria Azure, e duas maiorias de 3 learners se cruzam em ao menos um learner, esse learner viu a AWS no instante `t_c` ou depois: a Azure não consegue estar apta a promover antes de a AWS ter parado. A API `ForcePromote` **recusa** antes desse prazo. Continua sendo ação humana ([ADR-033](ADR-033-promocao-da-azure-humana.md)).
5. **Partição antes do `DEGRADE`** (os primeiros 2 s) segue a mesma regra: o lease já corre desde `t_c`, e a promoção espera D + M.
6. Com `DEGRADED_POLICY=reject` ([ADR-045](ADR-045-politica-de-degradacao-por-instalacao.md)) não há lease: a AWS nunca ACKa sem o gate da Azure, e a promoção se apoia na maioria Azure já estar na epoch nova (fencing por maioria, a provar no modelo).

## Alternativas consideradas

- **Sem modo degradado** (a partição para as escritas, RPO 0 sempre): coerente com o requisito original, mas a disponibilidade cai. Fica disponível por instalação com `DEGRADED_POLICY=reject`.
- **Como estava** (promoção humana e revogação de credenciais): não impede a escrita nas duas pontas.
- **Terceiro domínio de desempate:** descartado pela restrição dura.

## Consequências

- **Se a AWS morre de verdade em modo degradado, o RTO passa a ser pelo menos D + M.** É o preço de ter autoridade única sem terceiro domínio.
- Ao fim de D as escritas param mesmo que a Azure ainda esteja inalcançável: a disponibilidade em modo degradado passa a ser finita.
- A premissa é deriva de relógio limitada, como em todo lease. Usar relógio monotônico que conte o tempo de suspensão quando existir.
- Uma líder nova em degradado fica sem escrita até contato com a Azure ou `RENEW`: custo de disponibilidade aceito em troca de não estender o lease.
- **Sem contorno de emergência (break-glass) na v1** (Bruno, 13/10, 15b): a recusa do `ForcePromote` antes de D + M vale sempre, mesmo que uma pessoa comprove que a AWS foi destruída; o RTO no pior caso é D + M. Um contorno, exigindo fencing comprovado fora do software e duas pessoas, pode ser reavaliado depois do modelo TLA+ (PEND-21).
- **A prorrogação interage com a promoção:** prorrogar depois que a Azure já pode ser promovida reabre o risco. A regra (por exemplo, marcar a Azure como `promotion_blocked` antes de prorrogar) fica para o modelo TLA+ (hipótese H-DR13).
- A PEND-01 passa de "sem proposta" para "desenho decidido"; ela fecha quando D e M forem aprovados e o modelo provar a ausência de escrita nas duas pontas.

## Onde está o detalhe

- [C32-particao-e-lease-do-modo-degradado.md](../02_cenarios/atualizados/C32-particao-e-lease-do-modo-degradado.md)
- [X02-modo-degradado.md](../02_cenarios/complexos/X02-modo-degradado.md)
- [X03-desastre-e-promocao-da-azure.md](../02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md)

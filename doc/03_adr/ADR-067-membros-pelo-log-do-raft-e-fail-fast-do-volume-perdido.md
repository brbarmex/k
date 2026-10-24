# ADR-067 — Membros pelo log do Raft e fail-fast do volume perdido

**Status:** Decidido (Bruno, 18/10/2026: DD', DC-12, P-04, P-05); a troca de membros por ConfChange está no parking lot · **Data:** 18/10/2026 · **Registrado como ADR em:** 24/10/2026 · **Altera:** [ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md) (identidade nova), [ADR-043](ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md) (o que a configuração define) · **Afeta o estado atual:** seções 3.1 e 3.11

## Contexto

Se cada célula tira a lista de membros da própria configuração, uma célula nova com 5 membros e as antigas com 3 contam maiorias diferentes, e duas maiorias disjuntas podem eleger duas líderes. Uma célula que volta com o volume do Raft vazio e o **mesmo ID** esqueceu em quem votou e o que confirmou: pode perder uma escrita com ACK e votar duas vezes no mesmo termo.

## Decisão

1. **A configuração do deploy só cria o grupo (DD').** Depois, entrar ou sair é comando de administração pelo log do Raft (ConfChange): a célula nova entra sem voto, se reconstrói ([ADR-066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md)), alcança e só então vira votante; uma de cada vez. Os pods existentes não reiniciam.
2. **Fail-fast do volume perdido (DC-12):** uma célula com o volume do Raft vazio só cria o grupo quando ele está nascendo. Ela grava um token de partida no volume e pergunta aos pares: cria se todos os pares estão vazios ou se os que já rodam registraram o token dela no nascimento (`Genesis`), e se a tabela local está vazia. Com um par sem resposta, espera. Com sinal de grupo existente, não sobe.
3. **Devolver a célula ao grupo** (remover o ID antigo e adicionar um novo por ConfChange) é a feature `troca-de-pvc-em-cell-ativa` ([parking lot](../../parklot/adr-troca-de-pvc-em-cell-ativa.md)).
4. **Backup e restauração** do banco e do WAL ficam fora deste produto (P-04). Um backup do WAL nunca é restaurado numa célula de um grupo ativo.
5. **Ordem das entregas (P-05):** Incremento A → Incremento B → ConfChange e troca de PVC → multicloud.

## Alternativas consideradas

- **Membros só pela configuração** (RT-10 da POC): inseguro na troca em operação.
- **Reusar o ID com o volume vazio:** viola o [ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md).

## Consequências

- Até a ConfChange existir, uma célula que perde o volume fica fora até intervenção (na POC, `task clean`). O grupo de 3 tolera só mais uma falha nesse tempo.
- Restaurar um backup do WAL numa célula ativa equivale a perder o volume, e o fail-fast **não** detecta esse caso.
- A lista de membros passa a ser estado do log e vai no marcador ([ADR-065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md)).

## Na POC (código em 24/10/2026)

- **Fail-fast implementado na árvore local, sem commit:** `internal/domain/boot.go` (`DecideBoot`), `internal/adapter/out/etcdraft/boot.go` e `preBoot` em `internal/bootstrap/app.go`.
- **ConfChange não existe.** Os membros ainda vêm do `PEERS` e são conferidos contra o WAL na partida (`ErrMembership`, `internal/adapter/out/etcdraft/storage.go`): divergente do item 1, de propósito, até a feature do parking lot.

## Onde está o detalhe

- [decisoes.md](../../spec/compactacao_do_log/decisoes.md), rodadas 2c e 6
- [especificacao.md](../../spec/compactacao_do_log/especificacao.md), seções 2 e 7
- [parklot/adr-troca-de-pvc-em-cell-ativa.md](../../parklot/adr-troca-de-pvc-em-cell-ativa.md)

# ADR-066 — Réplica atrasada: canal de recuperação separado e reconstrução por sobrescrita no lugar

**Status:** Decidido (Bruno, 18/10/2026: D7, D9, DN, DC-02 a DC-04, DC-10, DC-11) · **Data:** 18/10/2026 · **Registrado como ADR em:** 24/10/2026 · **Substitui:** a reconstrução em tabela sombra do [ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md) (item 3) · **Altera:** [ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md) (reconstrução manual), [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md) (banco recriado) · **Afeta o estado atual:** seções 3.3, 3.5 e 3.11

## Contexto

Com o log limitado ([ADR-065](ADR-065-log-do-raft-limitado-snapshot-marcador-ram-e-disco.md)), uma réplica atrasada pode precisar de entradas que já saíram da RAM da líder, ou que já saíram do disco de todas as células. O Raft só oferece o `MsgSnap`, que aqui é um marcador sem dados.

## Decisão

1. **Estados da célula:** `normal`, `recuperando` e `reconstruindo`. Fora de `normal`, a célula não responde leitura forte (`503 CELL_RECOVERING`) e fica fora do `/readyz`.
2. **Canal de recuperação (D7, DC-02, DC-03):** a réplica só dá `Step` num `MsgSnap` com o aplicado ≥ índice do marcador. Antes disso, descarta e passa a `recuperando`. Escolhe uma fonte pelo `Retained` (célula em `normal` que ainda retém `aplicado + 1`, followers primeiro, depois o maior gate) e lê as entradas pelo fluxo `CatchUp`, em lotes, **só até o índice confirmado da fonte**. O `CommitPump` continua sendo o único alimentador do applier e, em recuperação, lê da fonte remota. Na follower, gate = `min(max(commit local, recuperado), recebido)`.
3. **Reconstrução (DC-04, DC-11):** quando nenhuma célula retém `aplicado + 1`, ou o progresso está atrás do disco retido na partida, a célula entra em `reconstruindo`. Confere `cluster_id` e `epoch` da fonte e o `KeySchema` de cada tabela: diferentes, **recusa** sem gravar nada e espera um novo disparo. A fonte fixa F (o aplicado no banco dela) e um pino em F − 1, que dura até o canal de recuperação da réplica começar ou até `REBUILD_PIN_TTL` (5 min, DC-10). A fonte envia as tabelas pelo fluxo `CopyTable` (Scan paginado com a versão); a réplica grava **na tabela que já tem**, pelo mesmo `PutItem` condicional por `_dr_v` do apply. Não apaga, não recria e não usa tabela sombra. No fim, grava o progresso F com fsync e **reinicia o processo**; a partida segue pela recuperação a partir de F + 1.
4. **Teto duro (D9, DN):** se a cópia passar do orçamento, o pino segura o disco da fonte até o teto duro; lá, a reconstrução é abortada e tenta de novo depois (`REBUILD_RETRY`), sobre a tabela que ficou. As escritas seguem.
5. **Disparo manual:** `POST /admin/reconstruir` (`task rebuild`), recusado na líder (409).

## Alternativas consideradas

- **Tabela sombra com cutover** (ADR-047, D8): substituída pela DC-11 (P-02 da matriz de garantias): sem troca de nome lógico, allowlist ou IAM para a sombra.
- **Apagar e recriar a tabela:** descartado; uma tentativa abortada deixaria a célula sem dados.
- **Recuperar só pelo `MsgApp` do Raft:** exigiria a líder segurar tudo na RAM.

## Consequências

- **DELETE real quebra a sobrescrita:** um item apagado na fonte continua vivo na tabela reconstruída. Quando o `DeleteItem` entrar, a lápide com `_dr_v` é obrigatória ([parking lot](../../parklot/adr-tombstone.md)). Hoje a POC não tem `DeleteItem`.
- **Outra história não tem caminho:** uma tabela com `epoch` diferente é recusada. Isso inclui a AWS que volta depois de uma promoção ([ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)), e contradiz o "banco recriado" do [ADR-042](ADR-042-aws-que-volta-boot-cercado-e-recriacao-da-celula.md). Em aberto.
- **Convergência sob carga não está provada:** se a cópia for mais lenta que a taxa de escrita, o pino chega ao teto duro e a reconstrução nunca termina. O custo é o tamanho da tabela, não o tempo fora (explicação em `explicacoes/celula_fora_por_dias.md`, na pasta compartilhada do projeto).
- **Pontos da crítica do prompt SPDD, sem resposta** (explicação em `explicacoes/prompt_spdd_compactacao_log.md`, na pasta compartilhada do projeto): C3, os pinos do `CatchUp` não têm teto e podem derrubar a líder; C5, uma célula em `recuperando` ou `reconstruindo` pode ser eleita líder e travar o grupo (o código não impede a candidatura); C6 e C7, recuperação e reconstrução podem não convergir sob carga; C8, a fonte da cópia com F atrás da célula.
- A reconstrução custa WCU no banco da célula e RCU no da fonte; no Cosmos, o mesmo protocolo precisa passar pelo Cloud-Adapter.

## Na POC (código em 24/10/2026)

**Implementado na árvore local, ainda sem commit** (Incremento B): `internal/usecase/recovery.go`, `rebuild.go`, `internal/domain/cellstate.go`, os fluxos `Retained`, `CatchUp` e `CopyTable` em `internal/adapter/in/peergrpc/server.go` e `api/peerv1`, e o `/admin/reconstruir` em `adminhttp`. O transporte entre células é gRPC sem TLS, com codec JSON e o limite padrão de mensagem de 4 MiB (análise de alto throughput em `explicacoes/poc_alto_throughput.md`, na pasta compartilhada do projeto, A2).

## Onde está o detalhe

- [especificacao.md](../../spec/compactacao_do_log/especificacao.md), seções 5 e 6
- [decisoes.md](../../spec/compactacao_do_log/decisoes.md), rodadas 4 e 6

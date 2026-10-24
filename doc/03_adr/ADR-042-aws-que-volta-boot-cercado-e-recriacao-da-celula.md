# ADR-042 — AWS que volta depois da promoção: boot cercado, credenciais revogadas e célula recriada

**Status:** Decidido (Bruno); **em conflito com os ADRs 064 e 066** desde 24/10/2026, aguarda decisão · **Data:** 13/10/2026

## Contexto

Depois da promoção da Azure (epoch E+1), os DynamoDBs da AWS guardam o estado de antes da queda, incluindo órfãs. A condição de versão (`v < índice`) não conhece a epoch: um applier antigo que volta na epoch E poderia aplicar o que faltava e piorar a divergência.

## Decisão

1. **Boot cercado (fail-closed):** a célula AWS sobe com os appliers **desligados** e só os liga depois de confirmar a epoch. Sem resposta da Azure, não aplica.
2. **Revogar as credenciais de escrita** (papel IAM) dos appliers antigos como passo da promoção.
3. A célula volta como learner com identidade nova ([ADR-034](ADR-034-fsync-fatal-e-identidade-nova.md)), com o banco marcado como suspeito, e o **DynamoDB da célula é recriado** a partir da réplica.
4. A epoch é checada no boot e periodicamente, **não por escrita** (checar por escrita custaria o dobro de WCU).

## Alternativas consideradas

- **Também fazer reconciliação dirigida das chaves órfãs:** mais rápida, mais difícil de provar correta. Fica fora da v1.

## Consequências

- A regra de cercar só vale quando a célula detecta mudança de epoch ou entradas além do `commitIndex` conhecido; uma reinicialização comum (deploy, nó) não pode ficar cercada.
- **Recriar o banco apaga as órfãs.** Elas precisam ser extraídas antes ([R13](../02_cenarios/a_rever/R13-escritas-orfas-reconciliacao.md)), e a recriação exige aprovação humana e backup (PITR) como rede de segurança.
- A revogação de credenciais depende do console da AWS estar acessível ([R11](../02_cenarios/a_rever/R11-api-de-promocao-depende-da-aws.md)).
- O retorno à AWS é uma transferência planejada depois da célula reconstruída e validada ([C16](../02_cenarios/atualizados/C16-aws-volta-apos-promocao.md)).

## Atualização de 13/10/2026 (revisão crítica)

O gatilho da promoção agora é o do [ADR-044](ADR-044-lease-do-modo-degradado-e-promocao-da-azure.md): a Azure só é promovida depois de D + M, então a AWS antiga já parou de aceitar escrita (lease expirado) antes de a Azure aceitar a primeira. O fencing por boot e credenciais continua necessário para o retorno da célula.

## Atualização de 24/10/2026 (redesenho de 18/10 e código da POC)

O gatilho mudou: não há D + M, a promoção é por comando ([ADR-064](ADR-064-topologia-3-aws-2-azure-confirmacao-k1-e-promocao-por-comando.md)). E o item 3 ("o DynamoDB da célula é recriado") conflita com a reconstrução por sobrescrita, que **nunca recria** e **recusa** uma fonte de outra história ([ADR-066](ADR-066-recuperacao-por-canal-separado-e-reconstrucao-por-sobrescrita.md), DC-11). A P-05 diz que a volta da AWS usa a ConfChange ([ADR-067](ADR-067-membros-pelo-log-do-raft-e-fail-fast-do-volume-perdido.md)). Como a AWS volta depois de uma promoção é ponto em aberto.

**Na POC:** não existe promoção nem retorno.

## Onde está o detalhe

- [C30-aws-que-volta-boot-cercado.md](../02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md)
- [X03-desastre-e-promocao-da-azure.md](../02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md)

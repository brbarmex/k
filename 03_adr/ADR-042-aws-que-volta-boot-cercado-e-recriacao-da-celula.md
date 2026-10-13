# ADR-042 — AWS que volta depois da promoção: boot cercado, credenciais revogadas e célula recriada

**Status:** Decidido (Bruno); depende da PEND-01 para o gatilho da promoção · **Data:** 13/10/2026

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

## Onde está o detalhe

- [C30-aws-que-volta-boot-cercado.md](../02_cenarios/atualizados/C30-aws-que-volta-boot-cercado.md)
- [X03-desastre-e-promocao-da-azure.md](../02_cenarios/complexos/X03-desastre-e-promocao-da-azure.md)

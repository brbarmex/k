# ADR-052 — Autorização por aplicação com mTLS

**Status:** Decidido (Bruno, revisão crítica de 13/10/2026) · **Data:** 13/10/2026

## Contexto

O [ADR-043](ADR-043-uma-instalacao-por-cliente-identidade-por-configuracao.md) respondeu "quem é o cliente", não "quem pode escrever o quê". Hoje cada aplicação do cliente tem o seu papel IAM, normalmente limitado às suas tabelas. Pelo proxy, o papel largo é o do `DataReplication`, e qualquer pod do namespace escreve em qualquer tabela: há **ampliação de privilégio** em relação ao cenário de hoje. O `Access Key Id` do SigV4 não resolve, pois a camada não valida a assinatura sem o segredo.

## Decisão

1. Cada aplicação do namespace tem **identidade de carga de trabalho** (certificado emitido pelo cert-manager) e fala **mTLS** com o `DataReplication`.
2. O `DataReplication` mantém, em configuração, uma lista **identidade → tabelas e operações permitidas**. Sem entrada, a resposta é `403`.
3. A NetworkPolicy do namespace continua como primeira barreira ([C20](../02_cenarios/atualizados/C20-bypass-de-escrita-direta.md)); o IAM das aplicações segue sem permissão de escrita nas tabelas.
4. O item 3 do ADR-043 passa a valer junto com este.

## Alternativas consideradas

- Só a NetworkPolicy do namespace: não separa uma aplicação de outra.
- Malha de serviço no cluster do cliente para entregar o mTLS sem mudar as aplicações: a avaliar caso a mudança nos SDKs pese.

## Consequências

- "Só trocar o endpoint" deixa de ser verdade: o cliente HTTP do SDK precisa apresentar o certificado. Entra no inventário de cada cliente ([ADR-051](ADR-051-compatibilidade-dynamodb-inventario-conformidade-e-idempotencia.md)).
- É preciso levantar as permissões IAM atuais de cada aplicação para montar a lista. Se todas compartilham hoje o mesmo papel, o ganho é menor, mas a lista continua valendo como registro.
- Renovação e revogação de certificados entram na operação da frota (risco 18).

## Onde está o detalhe

- [C34-autorizacao-por-aplicacao.md](../02_cenarios/atualizados/C34-autorizacao-por-aplicacao.md)
- [C31-implantacao-por-cliente-e-identidade.md](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md)

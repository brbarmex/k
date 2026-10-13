# ADR-043 — Uma instalação por cliente; o cliente é conhecido na configuração

**Status:** Decidido (Bruno) · **Data:** 13/10/2026

## Contexto

Era preciso definir como a API identifica o cliente (`Access Key Id` do SigV4 ou mTLS por aplicação). O Bruno esclareceu que a solução é **1 para 1**: no momento da configuração já se sabe qual é o time que usa o serviço, e cada time tem a sua conta AWS e a sua instalação, isolada.

## Decisão

1. **Cada cliente tem a sua instalação** (células, bancos e conta próprios). Não há multi-tenancy dentro de um `DataReplication`.
2. O cliente é identificado **pela configuração do deploy**, não por credencial em cada requisição. Nem o `Access Key Id` nem mTLS por aplicação são necessários para isso.
3. A fronteira entre a aplicação e o `DataReplication` continua sendo a **NetworkPolicy do namespace** do cliente; o IAM das aplicações fica sem permissão de escrita nas tabelas ([C20](../02_cenarios/atualizados/C20-bypass-de-escrita-direta.md)).
4. **Azure isolada por cliente** (assinatura e conta Cosmos próprias), espelhando a AWS (decidido em 13/10, pergunta 12a).
5. **Banda do cabo reservada ou limitada por cliente**: QoS no cabo e limite no `DataReplication`, com métrica por cliente (pergunta 13a).
6. As faixas de IP dos clientes **não se sobrepõem**, por um plano de endereçamento central (pergunta 14a); a premissa precisa ser confirmada com a rede e vale como requisito para entrar um cliente novo.
7. O resto da matriz de identidade (IRSA/workload identity para os bancos, mTLS entre células, porta de administração com MFA e auditoria) segue como padrão adotado.

## Alternativas consideradas

- **`Access Key Id` do SigV4 + NetworkPolicy:** o proxy não consegue validar a assinatura sem o segredo; serviria só como rótulo.
- **mTLS por aplicação:** custo de certificados sem ganho, já que o namespace pertence ao cliente.

## Consequências

- Reforça "plataforma multi-cliente fora da v1": cada instalação é um produto completo.
- **O custo é por cliente.** Seis gravações por escrita e o conjunto de clusters, bancos e circuitos se repetem a cada time ([R20](../02_cenarios/a_rever/R20-custo-6x.md)).
- **O cabo dedicado AWS↔Azure é compartilhado entre contas.** A replicação de um cliente pode saturar a banda dos outros, por isso a banda é reservada ou limitada por cliente (item 5) ([C25](../02_cenarios/atualizados/C25-link-lento-faixa-cinzenta.md)).
- O tráfego transitivo entre VPCs de contas diferentes exige **endereços IP que não se sobreponham** entre clientes (item 6).
- Versões, correções e a operação de N instalações viram um processo de frota; a definição de pronto vale por instalação.
- Recomendação: nome do cliente como variável de configuração, usada em rótulos de métricas, auditoria e chave de dedupe (nome da variável a definir).

## Atualização de 13/10/2026 (revisão crítica)

O item 3 (NetworkPolicy como fronteira) é completado pela **autorização por aplicação com mTLS** ([ADR-052](ADR-052-autorizacao-por-aplicacao-com-mtls.md)): o ADR-043 resolve "quem é o cliente", não "quem pode escrever o quê".

## Onde está o detalhe

- [C31-implantacao-por-cliente-e-identidade.md](../02_cenarios/atualizados/C31-implantacao-por-cliente-e-identidade.md)

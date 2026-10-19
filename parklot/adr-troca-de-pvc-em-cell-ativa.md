# ADR pendente — Troca de PVC em cell ativa

> **Status:** parking lot; não aprovada para implementação. Registrada em 18/10/2026
> a pedido do Bruno, ao decidir a P-03 da
> [matriz de garantias](../spdd/analysis/GGQPA-XXX-202610181805-[Analysis]-matriz-garantias-replicacao-rpo-disponibilidade-recuperacao.md).
> **Feature:** `troca-de-pvc-em-cell-ativa`.
> **Escopo:** devolver ao grupo, sem parar as outras células e sem perder escrita
> confirmada, uma célula que perdeu o volume do Raft (PVC) enquanto o grupo segue ativo.

## Resumo

Uma célula que volta com o volume do Raft vazio e o **mesmo ID** esqueceu as entradas
que confirmou e em quem votou. Se ela voltar a votar, o grupo pode perder uma escrita
que já teve ACK e pode eleger duas líderes no mesmo termo. O jeito seguro de devolvê-la
é **remover o ID antigo do grupo e adicionar a célula com um ID novo**, pela
ConfChange do Raft: primeiro como aprendiz (sem voto), depois de se reconstruir e
alcançar o log, como votante.

Até esta feature existir, a POC usa o **fail-fast** decidido na P-03 (DC-12 do canvas
de compactação): a célula com o volume vazio num grupo que já existe não sobe, e o log
explica o motivo e aponta para esta ADR.

## Contexto

### O problema

O Raft só é seguro porque cada célula guarda em disco, antes de responder:
- as entradas que confirmou (`MsgAppResp` só sai depois do fsync, `etcdraft/node.go:175-192`);
- o termo atual e o voto dado nesse termo (`HardState`).

Perder o volume apaga as duas coisas. Exemplos com 3 células:

1. **Escrita confirmada perdida (RPO>0).** A líder 1 grava uma escrita na 1 e na 3,
   que formam a maioria, e responde ACK. A 2 ainda não tem a escrita. A 3 perde o
   volume e volta vazia. A 1 cai. A 2 e a 3 elegem uma líder sem a escrita, que some.
2. **Duas líderes no mesmo termo.** A 3 votou na 1 no termo 7, perde o volume, volta
   e vota na 2 no mesmo termo 7. A 1 e a 2 têm maioria no mesmo termo e confirmam
   escritas diferentes.

### Quando acontece

- POC: só se alguém apagar o volume `cell-N-raft` na mão.
- Produção: depende do armazenamento. Com EBS é raro. Com disco local da instância
  (instance store), PVC recriado ou nó trocado, acontece a cada troca.

### O que o código faz hoje

- `etcdraft/storage.go:39-59`: sem WAL, a célula cria um do zero, com o snapshot
  sintético e o termo inicial, como se o grupo estivesse nascendo. Não avisa nada.
- A checagem `ErrMembership` (RT-10, `storage.go:71-74`) só roda quando já existe WAL.
- `PEERS` é fixo na configuração; a lista de membros não muda em operação.
- O ID do Raft (`NODE_ID`) e o nome da célula (`CELL_ID`) já são variáveis separadas
  no `docker-compose.yml`, mas hoje têm sempre o mesmo valor, e a tabela física é
  `cadastro_cliente_cell_<CELL_ID>`.

### Decisão provisória (POC): fail-fast (P-03, opção A; DC-12)

Na partida, antes de criar o WAL, uma célula com o volume do Raft vazio:
- pergunta aos pares, pelo `Retained`, se o grupo já existe (`cluster_id` igual e
  commit acima do snapshot sintético);
- olha se a própria tabela já tem itens com `_dr_v`.

No nascimento, cada célula vazia grava um token de partida (`boot-token`) no volume antes de responder, e quem cria o grupo registra os tokens que viu (`Genesis`). Assim uma célula fundadora que chega atrasada é reconhecida, e uma que perdeu o volume (e o token junto) não é.
Se houver qualquer sinal de grupo existente, ela **não sobe**: sai com erro e log
apontando para esta ADR. Na POC, a saída é `task clean`. Se algum par não responder,
ela espera em vez de criar um grupo. Só cria o grupo quando todos os pares respondem
com o volume vazio e a tabela local está vazia.

## Proposta para a feature

### Procedimento (célula 3 perdeu o volume)

| Passo | Ação | Grupo | Maioria | Tolera |
|---|---|---|---|---|
| 0 | A 3 está parada pelo fail-fast | 1, 2, ~~3~~ | 2 de 3 | 0 falhas na prática |
| 1 | **Remover:** `ConfChange RemoveNode(3)`, confirmada pela 1 e pela 2 | 1, 2 | 2 de 2 | 0 |
| 2 | **Adicionar como aprendiz:** `AddLearnerNode(4)`. A célula sobe no lugar da `cell-3`, com volume vazio e `NODE_ID=4`, em modo "entrar", sem criar grupo | 1, 2 + aprendiz 4 | 2 de 2 | 0 |
| 3 | A 4 recebe o log sem votar. Vazia, passa pela reconstrução (sobrescrita no lugar, DC-11) e pelo `CatchUp` | igual | 2 de 2 | 0 |
| 4 | **Promover:** `AddNode(4)` quando a 4 alcança a líder | 1, 2, 4 | 2 de 3 | 1 falha |

Uma célula com o ID 3 que apareça depois, com o disco velho, é ignorada: o ID não é
mais membro.

### Por que nessa ordem

- **Remover antes de adicionar:** com 4 votantes e um morto, a maioria passa a 3 de 4,
  e as escritas dependeriam da célula nova, ainda vazia.
- **Aprendiz antes de votante:** uma votante vazia entraria na conta da maioria sem
  conseguir confirmar nada.
- **Uma mudança por vez:** a forma simples da ConfChange no etcd-raft, a mesma da DD'
  para subir de 3 para 5 células.

### O que precisa ser construído

1. **ConfChange no `etcdraft`:** `ProposeConfChange`, `ApplyConfChange` no
   `handleReady`, e a `ConfState` resultante gravada no snapshot e no marcador.
2. **Comando de administração:** por exemplo `POST /admin/membros` (remover,
   adicionar aprendiz, promover), só na líder, com as tasks correspondentes.
3. **Endereços dos pares vindos do log:** hoje vêm de `PEERS`. A entrada de membro
   precisa levar o endereço junto, e o `peerclient` precisa abrir e fechar conexões
   em operação.
4. **Partida em modo "entrar":** a célula nova não cria grupo; espera a líder enviar o
   estado.
5. **Fim do `ErrMembership` como está (RT-10):** a configuração só vale na primeira
   criação do grupo (DD').
6. **ID do Raft separado do nome da célula:** o ID 4 continua usando a tabela e a porta
   da `cell-3`. Hoje `NODE_ID` e `CELL_ID` são iguais por convenção.
7. **Promoção segura:** só promover quando o aprendiz estiver dentro da janela e com o
   aplicado perto do commit da líder.
8. **IDs nunca reusados:** registro dos IDs já removidos, para que um ID antigo não
   volte por engano.

### Relação com outras decisões

- **DD'** (entrada de célula por ConfChange): é a mesma máquina. Esta feature é o caso
  de "entrar no lugar de outra"; a DD' é o de "entrar a mais".
- **DC-11** (reconstrução por sobrescrita): é o caminho de cópia que o aprendiz usa.
  Com a tabela da `cell-3` ainda cheia, os itens iguais não mudam. Os limites da
  DC-11 valem aqui (delete exige lápide; história diferente é recusada).
- **[adr-tombstone.md](adr-tombstone.md):** quando o delete entrar, a reconstrução do
  aprendiz precisa da lápide para não ressuscitar itens.
- **ADR-062** (banco com versão de outra história do log): a checagem da tabela
  local no fail-fast é o mesmo cuidado.

## Riscos

- **Janela sem tolerância a falha:** entre os passos 1 e 4 o grupo tem 2 votantes e
  não tolera perder mais nenhum. Com N=5 a janela é menos crítica (4 votantes,
  tolera 1).
- **Reconstrução lenta:** o tempo da janela é o tempo da cópia. Precisa ser medido com
  tabelas reais.
- **Operador remove a célula errada:** remover uma célula sã com outra já fora tira o
  quórum. O comando precisa recusar a remoção quando o grupo resultante não tiver
  maioria viva.
- **Restaurar um backup do WAL numa célula ativa:** o backup é de outro escopo (P-04). Um WAL antigo restaurado tem estado, então a célula sobe normalmente, mas esqueceu os votos e as entradas confirmadas depois do backup. É o mesmo perigo do volume perdido, e o fail-fast da DC-12 não pega esse caso, porque só olha volume vazio. Regra para quem faz o backup: um backup do WAL nunca é restaurado numa célula de um grupo que continua ativo; só serve para recriar o grupo inteiro, com `epoch` nova.
- **Duas mudanças de membro ao mesmo tempo:** o etcd-raft recusa uma ConfChange com
  outra pendente; o comando precisa expor isso de forma clara.

## Perguntas em aberto

1. O comando é só manual (operador) ou a automação pode disparar a troca ao detectar
   o volume vazio?
2. A célula nova reaproveita a tabela da célula antiga (mais rápido, pela DC-11) ou
   começa com tabela vazia?
3. Qual é o limite aceitável da janela sem tolerância a falha (passos 1 a 4)?
4. Em N=5, vale adicionar antes de remover para nunca ficar com tolerância zero?

## Critérios de saída

- Teste que apaga o volume de uma célula com escrita contínua, executa o procedimento
  e termina com 3 votantes, os mesmos itens e `_dr_v` nas 3 tabelas e nenhuma escrita
  com ACK perdida.
- Teste que um ID removido, ao voltar com o disco velho, é ignorado.
- Teste de recusa: remover uma célula quando a remoção deixaria o grupo sem maioria
  viva.
- O fail-fast da DC-12 continua valendo para quem não seguir o procedimento.

## Referências

- `DataReplicationApp/internal/adapter/out/etcdraft/storage.go`: criação do WAL com o
  volume vazio e `ErrMembership`.
- `DataReplicationApp/internal/adapter/out/etcdraft/node.go`: fsync antes do
  `MsgAppResp` e `RestartNode`.
- `DataReplicationApp/docker-compose.yml`: `NODE_ID`, `CELL_ID` e os volumes
  `cell-N-raft`.
- [decisoes.md](../spec/compactacao_do_log/decisoes.md): DD' e a rodada 2c.
- Canvas: [GGQPA-XXX-202610181712](../spdd/prompt/GGQPA-XXX-202610181712-[Feat]-service-compactacao-log-raft-recuperacao-reconstrucao.md), DC-11 e DC-12.

# Solução para AWS primária e Azure failover, com quórum de 3 a 7 células

Data: 18/10/2026. Origem: thread "Compactação do log: grill", cenários do Bruno (17:22). Decisões em [decisoes.md](decisoes.md).

Legenda: **[F]** fato do algoritmo (Raft, maiorias); **[I]** inferência, sem medição.

## O caso de uso

- AWS é a primária. A Azure só é usada quando a AWS fica totalmente fora.
- Composições: (1) 2 AWS + 1 Azure; (2) 3 AWS + 2 Azure; (3) 3 AWS; (4) 5 AWS; até 7 células, sempre com a **maioria na AWS**.
- Requisito (resposta A): RPO=0 na perda de uma célula/AZ **e** na perda do provedor inteiro.

## 1. O problema central: com a maioria na AWS, a maioria simples não basta

[F] O Raft confirma uma escrita quando a maioria das células gravou. Com a maioria na AWS, uma maioria pode ser formada **só por células AWS**:

| Composição | Maioria | Pode confirmar sem a Azure? |
|---|---|---|
| 2 AWS + 1 Azure | 2 de 3 | Sim (as 2 AWS) |
| 3 AWS + 2 Azure | 3 de 5 | Sim (as 3 AWS) |
| 4 AWS + 3 Azure | 4 de 7 | Sim (as 4 AWS) |

Se a AWS cair inteira logo depois, a escrita confirmada pode não estar em nenhuma célula da Azure. Isso é RPO>0, o que viola a resposta A.

Logo, no multicloud, **a regra de confirmação precisa ser mais forte que a maioria do Raft**:

> Uma escrita só é confirmada ao cliente quando estiver gravada (fsync) na maioria do Raft **e** em pelo menos K células da Azure.

Isso é o que o desenho antigo chamava de "gate". Chegamos a ele de novo, agora a partir do zero e pelo motivo certo: não é um detalhe da Azure, é a única forma de ter RPO=0 contra a perda do provedor quando o provedor primário tem a maioria. [F]

### Como implementar sem mexer no etcd-raft

[F] O etcd-raft só conhece maioria. A regra extra fica na aplicação:

- o Raft continua commitando por maioria (índice `commit`);
- cada célula Azure informa o índice que já tem com fsync;
- o índice confirmável é `min(commit, K-ésimo maior índice durável entre as células Azure)`;
- o ACK ao cliente **e o apply no banco** só acontecem até esse índice. Se o apply andasse antes, uma leitura poderia ver um dado que some se a AWS cair.

Três cuidados que isso exige:
- O índice confirmável nunca pode recuar, nem na troca de líder: a nova líder recalcula a partir do que as células Azure informam no termo dela. [F]
- Uma escrita commitada no Raft mas ainda não confirmada não é perdida em operação normal: ela segue no log e é confirmada quando a Azure gravar. Só é perdida se a AWS cair antes, e aí o cliente não recebeu ACK (recebeu timeout). Isso é permitido pelo RPO=0. [F]
- Latência: toda escrita espera a ida e volta AWS↔Azure mais o fsync da Azure. Com as duas em São Paulo e link dedicado, isso deve ficar em poucos milissegundos, mas precisa ser medido. [I]

## 2. Composição a composição

### Opção 1: 2 AWS + 1 Azure (N=3)

| Falha | Escritas | RPO |
|---|---|---|
| 1 célula AWS | continuam (1 AWS + Azure = maioria e K=1) | 0 |
| A célula Azure | **param**, ou entram no modo sem Azure (pergunta G) | 0 se param; >0 se seguem |
| AWS inteira | param; promoção humana da Azure | 0 |

Crítica [F + I]:
- A única célula Azure está em **toda** escrita. Um reinício dela, um deploy ou um fsync lento para as escritas (ou força o modo sem Azure). A célula vira ponto único de falha da disponibilidade.
- Depois do failover, a Azure tem **uma** célula só, sem redundância nenhuma: o próximo disco que falhar perde dado.
- Recomendação: não usar a opção 1 como multicloud de produção.

### Opção 2: 3 AWS + 2 Azure (N=5)

| Falha | Escritas | RPO |
|---|---|---|
| 1 ou 2 células AWS | continuam (sobra maioria com Azure) | 0 |
| 1 célula Azure | continuam com K=1 (a outra Azure cobre) | 0 |
| As 2 Azure | param, ou modo sem Azure (pergunta G) | 0 se param; >0 se seguem |
| AWS inteira | param; promoção humana | 0 com K=1, desde que se promova a célula Azure com o maior índice durável |

Com K=1, só uma das duas Azure tem a garantia da última escrita. Se ela falhar durante o próprio desastre, há perda. K=2 elimina isso, mas qualquer falha de uma Azure para as escritas. [F] Essa é a pergunta F.

### Opções 3 e 4: só AWS (3 ou 5 células)

- Maioria simples, sem regra extra. RPO=0 na perda de 1 célula (N=3) ou de 2 (N=5). [F]
- A perda da AWS inteira em São Paulo não tem RPO=0 por definição: não existe outro provedor. A resposta A só se aplica à parte de célula/AZ. [F]
- A compactação aqui é o caso clássico: só células AWS atrasadas seguram o log.

### Até 7 células

A regra generaliza: maioria do Raft + K células Azure. Exemplos: 5 AWS + 2 Azure (K=1), 4 AWS + 3 Azure (K=1 ou K=2).

## 3. A Azure vota ou não?

[F + I] Recomendo que as células Azure sejam **votantes**, mas que **nunca fiquem como líder**:
- Votando, elas entram na maioria. Na opção 1, sem o voto da Azure, a AWS teria N=2 e não tolera perder uma célula.
- Como a maioria está na AWS, a Azure sozinha nunca elege líder: não há duas líderes numa partição AWS×Azure.
- Se uma célula Azure vencer uma eleição (o Raft permite), ela transfere a liderança para uma célula AWS na hora. Liderança na Azure poria toda escrita do lado errado do link.

## 4. Failover para a Azure (AWS totalmente fora)

[F] Com a maioria na AWS, a Azure sozinha não forma quórum. A promoção é sempre forçada:
1. Uma pessoa confirma que a AWS está fora. Não dá para automatizar com segurança: a Azure não distingue "AWS morta" de "link cortado", e promover com a AWS viva cria duas líderes.
2. Escolhe-se a célula Azure com o maior índice durável. Ela tem tudo o que foi confirmado.
3. O grupo é recriado só com as células Azure, a partir dela. A lista de membros muda por decisão humana, não por quórum.
4. A AWS que volta não pode voltar a escrever com a história antiga: entra como célula nova (D'a), pelo caminho de reconstrução.

## 5. Onde a compactação entra

- Em operação normal, as células Azure estão em toda escrita, então quase não atrasam. O log retido é pequeno e a cauda em RAM resolve. [F]
- O atraso grande só existe quando as escritas seguem sem a Azure (pergunta G). É aí que valem as decisões D1, D2, D3 e D5: a Azure segura o log até o orçamento (~1 h), as entradas antigas ficam em disco, e depois disso ela sai da janela e se reconstrói.
- Nas opções só AWS, a compactação é o caso clássico do Raft.

## Perguntas abertas

Estão na thread: E (regra de confirmação), F (valor de K), G (Azure fora), H (liderança), I (opção 1) e J (promoção), mais 7 a 11 da rodada 2.

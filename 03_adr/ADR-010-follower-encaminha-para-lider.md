# ADR-010 — Follower encaminha para a líder; a liderança muda só por eleição de saúde

**Data:** 11/10/2026 · **Estado:** aceito pelo Bruno (11/10/2026) · **Substitui:** a semântica original de ADR-010 ("transferência em acesso a follower")

## Contexto

O requisito original dizia que, quando o gateway encaminha uma requisição a uma célula follower, essa célula deve se tornar líder e a liderança anterior deve ser revogada. Esse requisito existia por dois motivos:

1. **Roteamento do gateway:** o gateway pode mandar tráfego para uma célula que não é a líder, por cache desatualizado, balanceamento ou falha parcial.
2. **Célula líder unhealthy:** a líder pode estar degradada, e o gateway desvia o tráfego dela.

Restrições que valem para esta decisão: RPO = 0; overhead da camada ≤ 50ms por transação completa; clientes críticos com ~3000 TPS; failover automático.

## Decisão

- Uma follower que recebe uma escrita **encaminha a requisição para a líder atual** (proxy interno, no mesmo termo/epoch) e devolve a resposta ao cliente. Ela não executa a escrita localmente e não inicia uma troca de liderança.
- A liderança muda **somente** por:
  - **eleição por saúde** do protocolo de consenso, quando a líder perde o quórum ou deixa de mandar heartbeats dentro do election timeout; ou
  - **transferência planejada** (manutenção, rebalanceamento, failback), feita por um operador ou pelo control plane, com histerese/cooldown.
- O gateway é tratado como **dica de roteamento**, nunca como autoridade de liderança. As respostas da camada incluem o líder e o termo atuais, para que o gateway corrija o próprio cache.
- Se a follower não alcança a líder e não há eleição concluída, ela responde **erro recuperável** (`retryAfter`), e nunca um sucesso local.

## Por que não "follower vira líder"

| Motivo | Explicação |
|---|---|
| **Uma requisição não é sinal de saúde** | O gateway pode estar particionado da líder, enquanto a líder segue saudável e com quórum. Promover a follower por causa do ponto de vista do gateway troca um problema de rede do gateway por uma troca de liderança desnecessária. |
| **Custo de latência incompatível com o orçamento** | Uma transferência segura exige barreira, catch-up do log, novo termo e fencing, o que custa centenas de ms. O orçamento total é de 50ms por transação. Já o encaminhamento custa ~1–2ms dentro da mesma região. |
| **Thrashing** | Com o gateway espalhando tráfego entre células, cada requisição numa célula diferente moveria a liderança. Com 3000 TPS, a escrita ficaria parada em transições a maior parte do tempo. |
| **Indisponibilidade a cada troca** | Durante a transferência, as escritas do shard pausam. Trocas frequentes multiplicam essa janela. |
| **Risco de segurança (split-brain)** | Cada promoção é uma oportunidade de duas autoridades se sobreporem se o fencing tiver qualquer falha. Menos trocas significa menos exposição. |
| **"Líder unhealthy" já tem mecanismo próprio** | A eleição por health/heartbeat detecta uma líder morta ou sem quórum de forma consistente para todos os votantes, e não só pelo ponto de vista de um cliente. |
| **Prática da indústria** | Raft, etcd, Consul, CockroachDB e Spanner encaminham para a líder (ou rejeitam com redirecionamento). Nenhum promove por requisição recebida. |

## Consequências

- **Positivas:** latência previsível (+1–2ms no caminho errado); liderança estável; menos janelas de indisponibilidade; superfície menor para split-brain.
- **Negativas:** um gateway mal configurado gera um salto extra persistente até corrigir o próprio cache, então é preciso uma métrica (`forwarded_writes_ratio`) com alerta. Uma líder "lenta mas viva" (degradação parcial sem perda de heartbeat) não é trocada automaticamente: é preciso definir critérios de health além do heartbeat (ex.: latência de fsync/apply acima de um limiar por N segundos dispara uma transferência planejada).
- **Pendência:** definir o critério de "líder degradada" que dispara a transferência automática planejada, com histerese e cooldown.

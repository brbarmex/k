# ADR-057 — Um único modo: strong consistency

**Status:** Decidido (Bruno, 13/10/2026) · **Data:** 13/10/2026 · **Substitui:** os antigos ADR-009, ADR-012, ADR-023, ADR-040 e ADR-053, removidos do pacote

## Contexto

O pacote descrevia dois modos no `DataReplication`: Strong e Eventual. O Eventual já tinha perdido o apply paralelo e virado só um modo de leitura, mas continuava espalhado pela escrita, pela replicação, pelo Raft, pela leitura nas células da Azure e pela configuração (`READ_MODE_DEFAULT`). O Bruno decidiu: **o `DataReplication` tem um único modo, strong consistency**, sem segundo fluxo, e **as aplicações sempre enviam `ConsistentRead=true`**.

## Decisão

1. **Um fluxo só, na escrita, na replicação e no Raft.** A líder propõe, o Raft commita (2 de 3 da AWS com fsync), o gate espera a maioria da Azure com fsync ([ADR-005](ADR-005-ack-exige-gate.md), [ADR-017](ADR-017-azure-learners-e-gate.md)), o applier aplica **só depois do gate**, e o ACK sai depois do apply na célula da líder. Não existe apply antes do gate, nem modo por tabela, nem modo por instalação. "Depois do gate" é a regra; dentro dela, os itens são gravados em paralelo por chave, com a mesma chave na ordem do log e o `applied` como marca d'água contígua ([ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md), 14/10: a primeira redação dizia "em série", e o código leu "um item por vez").
2. **Leitura sempre forte na tabela base.** A leitura espera o `appliedIndex` da célula alcançar a barreira `min(ReadIndex, gate)` ([ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md)). As aplicações enviam `ConsistentRead=true`; uma leitura de tabela base que chegue sem o parâmetro também é servida como forte, porque não existe outro caminho (métrica `read_without_consistent_read`, para achar a aplicação fora do contrato).
3. **Nenhuma leitura é servida sem a barreira.** Quando a barreira não é alcançada dentro do prazo (sem líder, partição, applier atrasado), a resposta é erro recuperável (`503`), nunca dado possivelmente velho.
4. **Células da Azure antes da promoção** seguem a mesma regra de qualquer célula seguidora: pedem `ReadIndex` e gate à líder AWS e esperam o próprio banco alcançar a barreira. Sem líder alcançável (link caído, modo degradado), recusam com `503`. Não há leitura de dado defasado com aviso.
5. **Configuração:** a variável `READ_MODE_DEFAULT` sai, assim como `WRITE_MODE` e `MAX_STALENESS`. Uma configuração que ainda as define falha na partida.
6. **Query em GSI é recusada** ([ADR-050](ADR-050-query-em-gsi-recusada.md), decisão do Bruno): o índice global do DynamoDB nunca é forte, então não pode existir no modo único.

## Alternativas consideradas

- **GSI como exceção documentada:** descartado pelo Bruno em favor de recusar ([ADR-050](ADR-050-query-em-gsi-recusada.md)).
- **Manter o Eventual só como modo de leitura** (estado anterior): dois caminhos de leitura, uma variável obrigatória e um cenário de leitura defasada na Azure para pouco ganho de latência.
- **Recusar a leitura sem `ConsistentRead=true`:** força o contrato, mas transforma um esquecimento da aplicação em indisponibilidade. A métrica cumpre o papel de achar quem está fora do contrato.

## Consequências

- **Custo:** no DynamoDB sob demanda a leitura forte consome o dobro de unidades de leitura da eventual. Entra na estimativa por cliente ([R20](../02_cenarios/a_rever/R20-custo-6x.md)).
- **Latência:** toda leitura numa célula seguidora paga uma ida à líder (`ReadIndex` e gate na mesma resposta); o `ReadIndex` em lote reduz o custo sob carga ([R16](../02_cenarios/a_rever/R16-leitura-forte-em-celulas-follower.md)).
- **Disponibilidade da leitura:** sem líder conhecida, a leitura espera até 1 s e devolve `503`, como a escrita ([ADR-039](ADR-039-sem-lider-conhecida-espera-ate-1s-e-503.md)). Um applier atrasado (throttling do banco) atrasa as leituras daquela célula.
- **TTL:** cada célula expira por conta própria e o DynamoDB apaga com atraso (até dias). Duas leituras fortes em células diferentes podem discordar sobre um item já vencido; a aplicação filtra pelo atributo de TTL, como o próprio DynamoDB recomenda ([X06](../02_cenarios/complexos/X06-delete-ttl-gsi.md)).
- **Scan:** a leitura forte espera a barreira antes de cada página, mas não há instantâneo entre páginas (como no DynamoDB).
- **Aplicações que usam Query em GSI** precisam mudar o padrão de acesso antes de entrar na camada ([ADR-050](ADR-050-query-em-gsi-recusada.md)).
- **Menos código e menos teste:** some o segundo caminho no applier, na leitura e na configuração, e o cenário de leitura defasada na Azure.
- O apply sempre depois do gate, com o `applied` como marca d'água contígua ([ADR-058](ADR-058-apply-paralelo-por-chave-com-marca-dagua.md)), mantém `applied ≤ gate`, que sustenta a barreira da leitura ([ADR-049](ADR-049-barreira-da-leitura-forte-e-o-gate.md)) e a invariante `I-DEL` ([ADR-047](ADR-047-delete-real-invariante-e-reconstrucao-em-tabela-sombra.md)).

## Onde está o detalhe

- [06_leitura_forte_escrita_serial_e_latencia.md](../01_arquitetura/06_leitura_forte_escrita_serial_e_latencia.md)
- [C03-escrita-strong.md](../02_cenarios/atualizados/C03-escrita-strong.md)
- [C05-leitura-strong.md](../02_cenarios/atualizados/C05-leitura-strong.md)

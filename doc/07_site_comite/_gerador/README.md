# Gerador do site do comitê

`python3 gerar_site.py` (requer `pip install markdown`) regera `07_site_comite/` a partir do pacote.
`pontos/*.json` guarda o texto dos "Pontos que precisam ser avaliados" (contexto, o que a fonte define e sugestão do arquiteto).
`adr_resumo.py` guarda a decisão resumida de cada ADR. A classificação success/fail fica em `SUCCESS` no gerador.

`python3 gerar_documento_unico.py` regera `DataReplication_Versao_1_Documento_Unico.html` na raiz do pacote: todos os Markdown num só arquivo, links internos como âncoras e diagramas em Mermaid.

O site tem duas páginas a partir de documentos consolidados: `estado-atual.html` (de `ESTADO_ATUAL.md`) e `inventario-adrs.html` (de `03_adr/INVENTARIO.md`). A coluna "Status efetivo" das tabelas de ADR de cada cenário vem da tabela do inventário. O documento único abre com o estado atual e traz o inventário antes dos ADRs.

`python3 checar_estado.py` (ou `task check-estado` em `DataReplicationApp`) falha se algum ADR não estiver citado no `ESTADO_ATUAL.md`, sem linha no inventário ou sem resumo em `adr_resumo.py`, ou se o "cobre ADR-001 a ADR-0NN" do estado atual não for o último ADR. Rode antes de regerar o site.

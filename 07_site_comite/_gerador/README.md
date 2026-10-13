# Gerador do site do comitê

`python3 gerar_site.py` (requer `pip install markdown`) regera `07_site_comite/` a partir do pacote.
`pontos/*.json` guarda o texto dos "Pontos que precisam ser avaliados" (contexto, o que a fonte define e sugestão do arquiteto).
`adr_resumo.py` guarda a decisão resumida de cada ADR. A classificação success/fail fica em `SUCCESS` no gerador.

`python3 gerar_documento_unico.py` regera `DataReplication_Versao_1_Documento_Unico.html` na raiz do pacote: todos os Markdown num só arquivo, links internos como âncoras e diagramas em Mermaid.

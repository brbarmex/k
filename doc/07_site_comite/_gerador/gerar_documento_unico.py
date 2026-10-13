#!/usr/bin/env python3
"""Gera DataReplication_Versao_1_Documento_Unico.html (raiz do pacote) a partir dos Markdown.

Reúne num só arquivo o documento principal, a arquitetura, todos os cenários, os ADRs, os
diagramas, a implementação e as pendências. Links entre arquivos do pacote viram âncoras
internas; links para outros arquivos (código) continuam relativos. Diagramas só em Mermaid,
renderizados no navegador (mermaid 11.4.1, como o site do comitê).
Uso: python3 gerar_documento_unico.py   (requer `pip install markdown`)
"""
import datetime, glob, html, os, re
import markdown

PKG = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SAIDA = os.path.join(PKG, "DataReplication_Versao_1_Documento_Unico.html")
MERMAID_CDN = "https://cdn.jsdelivr.net/npm/mermaid@11.4.1/dist/mermaid.min.js"
HOJE = datetime.date.today().strftime("%d/%m/%Y")


def ler(p):
    with open(p, encoding="utf-8") as f:
        return f.read()


def ordenados(padrao, sem_indice=True):
    fs = sorted(glob.glob(os.path.join(PKG, padrao)))
    if sem_indice:
        fs = [f for f in fs if os.path.basename(f) not in ("INDEX.md", "README.md")]
    return fs


# (id da parte, título, arquivos)
PARTES = [
    ("p-principal", "Documento principal", [os.path.join(PKG, "DataReplication_Versao_1.md")]),
    ("p-arquitetura", "Arquitetura", ordenados("01_arquitetura/*.md")),
    ("p-cenarios", "Cenários atualizados",
     [os.path.join(PKG, "02_cenarios", "INDEX.md")] + ordenados("02_cenarios/atualizados/*.md")),
    ("p-complexos", "Cenários complexos", ordenados("02_cenarios/complexos/*.md")),
    ("p-rever", "Cenários a rever", ordenados("02_cenarios/a_rever/*.md")),
    ("p-adr", "ADRs", ordenados("03_adr/ADR-*.md")),
    ("p-diagramas", "Diagramas de arquitetura", ordenados("04_diagramas/D*.md")),
    ("p-implementacao", "Implementação", ordenados("05_implementacao/*.md")),
    ("p-pendencias", "Pendências e riscos", ordenados("06_pendencias_e_riscos/*.md")),
]

DOCS = {}  # caminho real -> id do documento
for _, _, fs in PARTES:
    for f in fs:
        rel = os.path.relpath(f, PKG)
        DOCS[os.path.realpath(f)] = "d-" + re.sub(r"[^a-z0-9]+", "-", rel.lower().removesuffix(".md")).strip("-")


def slug(h):
    """Mesmo slug que os links do pacote usam (estilo GitHub)."""
    h = re.sub(r"<[^>]+>", "", h)
    h = html.unescape(h).strip().lower()
    h = re.sub(r"[`*_~\[\]()<>]", "", h)
    h = re.sub(r"[^\w\- ]", "", h)
    return h.replace(" ", "-")


def reescrever_links(md, md_dir):
    def sub(m):
        texto, alvo = m.group(1), m.group(2)
        if re.match(r"^[a-z]+:", alvo):
            return m.group(0)
        caminho, _, ancora = alvo.partition("#")
        if not caminho:
            return m.group(0)  # âncora interna, tratada depois com o id do documento
        abs_ = os.path.realpath(os.path.join(md_dir, caminho))
        if abs_ in DOCS:
            did = DOCS[abs_]
            return f"[{texto}](#{did}--{ancora})" if ancora else f"[{texto}](#{did})"
        rel = os.path.relpath(abs_, PKG).replace(os.sep, "/")
        return f"[{texto}]({rel}{'#' + ancora if ancora else ''})"
    return re.sub(r"\[([^\]]*)\]\(([^)\s]+)\)", sub, md)


def render(path):
    did = DOCS[os.path.realpath(path)]
    md = ler(path)
    titulo = md.split("\n", 1)[0].lstrip("# ").strip()
    md = md.split("\n", 1)[1] if md.startswith("# ") else md
    md = reescrever_links(md, os.path.dirname(path))
    blocos = []

    def guarda(m):
        blocos.append(m.group(1))
        return f"\n\nMERMAIDBLOCO{len(blocos) - 1}FIM\n\n"
    md = re.sub(r"```mermaid\n(.*?)```", guarda, md, flags=re.S)
    h = markdown.markdown(md, extensions=["tables", "fenced_code", "sane_lists"])
    for k, b in enumerate(blocos):
        h = h.replace(f"<p>MERMAIDBLOCO{k}FIM</p>",
                      f'<div class="diagrama"><pre class="mermaid">{html.escape(b.strip())}</pre></div>')
    # Títulos: h2 do arquivo vira h4 (o arquivo é h3 dentro da parte h2), com id prefixado.
    vistos = {}

    def titulo_sub(m):
        nivel, conteudo = int(m.group(1)), m.group(2)
        s = slug(conteudo)
        n = vistos.get(s, 0)
        vistos[s] = n + 1
        s = s if n == 0 else f"{s}-{n}"
        novo = min(nivel + 2, 6)
        return f'<h{novo} id="{did}--{s}">{conteudo}</h{novo}>'
    h = re.sub(r"<h([1-6])>(.*?)</h\1>", titulo_sub, h, flags=re.S)
    h = re.sub(r'href="#(?!d-|p-)([^"]+)"', lambda m: f'href="#{did}--{m.group(1)}"', h)
    h = h.replace("<table>", '<div class="tabela"><table>').replace("</table>", "</table></div>")
    rel = os.path.relpath(path, PKG).replace(os.sep, "/")
    return did, titulo, (f'<article class="doc" id="{did}"><h3>{html.escape(titulo)}</h3>'
                         f'<p class="fonte">Fonte: <a href="{rel}">{rel}</a></p>{h}</article>')


def main():
    toc, corpo, n_diag = [], [], 0
    for pid, ptit, fs in PARTES:
        itens, docs = [], []
        for f in fs:
            did, tit, h = render(f)
            n_diag += h.count('class="mermaid"')
            docs.append(h)
            itens.append(f'<li><a href="#{did}">{html.escape(tit)}</a></li>')
        toc.append(f'<li><details><summary><a href="#{pid}">{ptit}</a> <span class="qtd">{len(fs)}</span></summary>'
                   f'<ul>{"".join(itens)}</ul></details></li>')
        corpo.append(f'<section id="{pid}"><h2>{ptit}</h2>{"".join(docs)}</section>')

    pagina = f"""<!doctype html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>DataReplication v1: documento único</title>
<style>{CSS}</style>
</head>
<body>
<div class="wrap">
<nav class="toc" aria-label="Sumário">
<a class="t" href="#topo">DataReplication v1</a>
<ul>{"".join(toc)}</ul>
</nav>
<main id="topo">
<h1>DataReplication v1: documento único</h1>
<div class="meta">Gerado em {HOJE} a partir do pacote em Markdown · Dono: Bruno · {sum(len(p[2]) for p in PARTES)} documentos e {n_diag} diagramas Mermaid.
Os links entre documentos levam às seções deste arquivo; os links para código levam aos arquivos do pacote.
Em caso de divergência, vale o Markdown do pacote.</div>
{"".join(corpo)}
<footer><p>Gerado por <code>07_site_comite/_gerador/gerar_documento_unico.py</code>. <a href="#topo">Voltar ao início</a></p></footer>
</main>
</div>
<script src="{MERMAID_CDN}"></script>
<script>
(function(){{
  var escuro = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
  mermaid.initialize({{startOnLoad:false, theme: escuro ? 'dark' : 'default', securityLevel:'strict'}});
  var pend = Array.prototype.slice.call(document.querySelectorAll('pre.mermaid'));
  if (!('IntersectionObserver' in window)) {{ mermaid.run({{nodes: pend}}); return; }}
  var obs = new IntersectionObserver(function(es){{
    es.forEach(function(e){{ if (e.isIntersecting) {{ obs.unobserve(e.target); mermaid.run({{nodes:[e.target]}}); }} }});
  }}, {{rootMargin:'800px 0px'}});
  pend.forEach(function(n){{ obs.observe(n); }});
}})();
</script>
</body>
</html>
"""
    with open(SAIDA, "w", encoding="utf-8") as f:
        f.write(pagina)
    print(f"{SAIDA}: {sum(len(p[2]) for p in PARTES)} documentos, {n_diag} diagramas")


CSS = r"""
:root{--bg:#fff;--fg:#1c2733;--mut:#5b6b7b;--line:#d9e0e8;--card:#f6f8fb;--acc:#1f4e8c;--link:#0b5cad;--code:#eef1f5}
@media (prefers-color-scheme:dark){:root:not([data-theme=light]){--bg:#10161d;--fg:#e5ebf1;--mut:#9aa9b8;--line:#2b3744;--card:#18212b;--acc:#7fb2ee;--link:#8cc0ff;--code:#1f2a36}}
:root[data-theme=dark]{--bg:#10161d;--fg:#e5ebf1;--mut:#9aa9b8;--line:#2b3744;--card:#18212b;--acc:#7fb2ee;--link:#8cc0ff;--code:#1f2a36}
*{box-sizing:border-box}
html{scroll-behavior:smooth;scroll-padding-top:16px;-webkit-text-size-adjust:100%}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
a{color:var(--link)}
code{font:.88em ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;background:var(--code);padding:1px 4px;border-radius:4px;overflow-wrap:anywhere}
pre{background:var(--code);padding:12px 14px;border-radius:8px;overflow:auto;font-size:14px;line-height:1.45}
pre code{background:none;padding:0}
.wrap{display:grid;grid-template-columns:320px minmax(0,1fr);max-width:1500px;margin:0 auto}
nav.toc{position:sticky;top:0;align-self:start;height:100vh;overflow:auto;padding:20px 14px;border-right:1px solid var(--line);font-size:14px}
nav.toc ul{list-style:none;margin:0;padding:0}
nav.toc ul ul{margin:2px 0 10px 8px;padding-left:10px;border-left:1px solid var(--line)}
nav.toc li{margin:3px 0}
nav.toc a{text-decoration:none;color:var(--fg)}
nav.toc a:hover{color:var(--link)}
nav.toc summary{cursor:pointer;font-weight:600}
nav.toc .t{font-weight:700;margin-bottom:10px;display:block}
.qtd{font-weight:400;color:var(--mut);font-size:12px}
main{padding:28px 40px 80px;min-width:0}
h1{font-size:30px;line-height:1.25;margin:0 0 6px}
h2{font-size:26px;margin:56px 0 12px;padding-top:14px;border-top:3px solid var(--acc)}
h3{font-size:21px;margin:40px 0 4px;padding-top:10px;border-top:1px solid var(--line)}
h4{font-size:18px;margin:28px 0 8px}h5{font-size:16px;margin:20px 0 6px}h6{font-size:15px;margin:16px 0 6px}
.meta{color:var(--mut);font-size:14px;margin-bottom:18px;background:var(--card);border:1px solid var(--line);border-radius:8px;padding:10px 14px}
.fonte{color:var(--mut);font-size:13px;margin:0 0 12px}
.tabela{overflow-x:auto;margin:10px 0 18px}
table{border-collapse:collapse;width:100%;font-size:15px}
th,td{border:1px solid var(--line);padding:7px 10px;text-align:left;vertical-align:top}
th{background:var(--card)}
blockquote{margin:14px 0;padding:8px 16px;border-left:4px solid var(--acc);background:var(--card);border-radius:0 8px 8px 0}
.diagrama{overflow-x:auto;background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px;margin:14px 0}
pre.mermaid{background:none;margin:0;text-align:center}
footer{margin-top:60px;color:var(--mut);font-size:14px;border-top:1px solid var(--line);padding-top:12px}
@media (max-width:900px){.wrap{display:block}nav.toc{position:static;height:auto;border-right:0;border-bottom:1px solid var(--line)}main{padding:20px 16px 60px}}
"""

if __name__ == "__main__":
    main()

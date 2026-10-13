#!/usr/bin/env python3
"""Gera o site estático do comitê a partir de DataReplication_Versao_1/."""
import html, json, os, re, sys, glob, datetime
import markdown

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from adr_resumo import RESUMO

# Raiz do pacote: duas pastas acima deste arquivo (07_site_comite/_gerador/).
PKG = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(PKG, "07_site_comite")
PONTOS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "pontos")
MERMAID_CDN = "https://cdn.jsdelivr.net/npm/mermaid@11.4.1/dist/mermaid.min.js"
HOJE = datetime.date.today().strftime("%d/%m/%Y")

SUCCESS = {"C01", "C02", "C03", "C05", "C06", "C11", "C18", "C23", "C31"}

CATS = [
    ("success-path", "Success path", "Fluxos em regime normal, sem falha."),
    ("fail-path", "Fail path", "Falhas e degradações com tratamento decidido."),
    ("complex-path", "Complex path", "Cenários compostos e aprofundados."),
    ("parking-lot", "Parking lot", "Riscos e questões ainda sem decisão fechada."),
]
CAT_NOME = {c[0]: c[1] for c in CATS}

# Diagrama de arquitetura geral relacionado (04_diagramas): (arquivo D, índice do bloco mermaid).
# Escolha do site, não indicada pelos cenários.
D_REL = {
    "C01": ("D01", 0), "C02": ("D02", 0), "C03": ("D05", 0), "C05": ("D05", 0),
    "C06": ("D01", 0), "C07": ("D07", 0), "C08": ("D05", 0), "C09": ("D05", 0),
    "C10": ("D01", 0), "C11": ("D06", 0), "C12": ("D06", 0), "C13": ("D04", 0),
    "C14": ("D01", 0), "C15": ("D06", 0), "C16": ("D06", 0), "C18": ("D04", 0),
    "C19": ("D02", 0), "C20": ("D02", 0), "C21": ("D04", 0), "C23": ("D08", 0),
    "C24": ("D07", 0), "C25": ("D05", 0), "C26": ("D08", 0), "C27": ("D07", 0),
    "C29": ("D04", 0), "C30": ("D06", 0), "C31": ("D02", 0), "C32": ("D06", 0),
    "C33": ("D08", 0), "C34": ("D02", 0),
    "X01": ("D05", 0), "X02": ("D06", 0), "X03": ("D01", 0), "X04": ("D04", 0),
    "X05": ("D07", 0), "X06": ("D08", 0),
    "R03": ("D08", 0), "R06": ("D05", 0), "R07": ("D06", 0), "R09": ("D04", 0),
    "R11": ("D01", 0), "R12": ("D06", 0), "R16": ("D05", 0), "R20": ("D08", 0),
    "R23": ("D01", 0),
}

# ---------------------------------------------------------------- leitura

def ler(p):
    with open(p, encoding="utf-8") as f:
        return f.read()

def parse_md(path):
    txt = ler(path)
    linhas = txt.split("\n")
    titulo = linhas[0].lstrip("# ").strip()
    partes = re.split(r"^## (.+)$", txt, flags=re.M)
    cab = partes[0]
    secs = []
    for i in range(1, len(partes), 2):
        secs.append((partes[i].strip(), partes[i + 1].strip("\n")))
    status = ""
    for l in cab.split("\n")[1:]:
        if l.strip():
            status = l.strip()
            break
    return {"titulo": titulo, "status": status, "secs": secs, "path": path}

def adrs_do_cabecalho(status):
    m = re.search(r"\*\*ADRs:\*\*(.*?)(?:·\s*\*\*Relacionados|$)", status)
    if not m:
        return []
    ids = []
    for n in re.findall(r"ADR-(\d{3})", m.group(1)):
        if n not in ids:
            ids.append(n)
    return ids

def relacionados(status):
    m = re.search(r"\*\*Relacionados:\*\*(.*)$", status)
    return m.group(1).strip() if m else ""

def status_texto(status):
    s = re.split(r"·\s*\*\*ADRs:\*\*", status)[0]
    s = re.sub(r"^\*\*Status:\*\*\s*", "", s).strip()
    return s

ADR_ARQ = {}
ADR_TIT = {}
for p in sorted(glob.glob(os.path.join(PKG, "03_adr", "ADR-*.md"))):
    n = re.match(r"ADR-(\d{3})", os.path.basename(p)).group(1)
    ADR_ARQ[n] = p
    t = ler(p).split("\n")[0].lstrip("# ").strip()
    ADR_TIT[n] = re.sub(r"^ADR-\d{3}\s*—\s*", "", t)

# ---------------------------------------------------------------- páginas e links

PAGINAS = {}   # id -> dict(cat, arquivo html relativo a OUT, md)

def registrar():
    for p in sorted(glob.glob(os.path.join(PKG, "02_cenarios", "atualizados", "C*.md"))):
        i = os.path.basename(p)[:3]
        cat = "success-path" if i in SUCCESS else "fail-path"
        PAGINAS[i] = {"cat": cat, "md": p}
    for p in sorted(glob.glob(os.path.join(PKG, "02_cenarios", "complexos", "X*.md"))):
        PAGINAS[os.path.basename(p)[:3]] = {"cat": "complex-path", "md": p}
    for p in sorted(glob.glob(os.path.join(PKG, "02_cenarios", "a_rever", "R*.md"))):
        PAGINAS[os.path.basename(p)[:3]] = {"cat": "parking-lot", "md": p}
    for i, d in PAGINAS.items():
        nome = os.path.splitext(os.path.basename(d["md"]))[0] + ".html"
        d["html"] = f'{d["cat"]}/{nome}'
        d["info"] = parse_md(d["md"])
registrar()
MD_PARA_ID = {os.path.realpath(d["md"]): i for i, d in PAGINAS.items()}

def href_de(pagina_dir, alvo_abs):
    return os.path.relpath(alvo_abs, pagina_dir).replace(os.sep, "/")

def reescrever_links(md, md_dir, pagina_dir):
    def sub(m):
        texto, alvo = m.group(1), m.group(2)
        if re.match(r"^[a-z]+:", alvo) or alvo.startswith("#"):
            return m.group(0)
        caminho, _, ancora = alvo.partition("#")
        abs_ = os.path.realpath(os.path.join(md_dir, caminho))
        if abs_ in MD_PARA_ID:
            dest = os.path.join(OUT, PAGINAS[MD_PARA_ID[abs_]]["html"])
            return f"[{texto}]({href_de(pagina_dir, dest)})"
        if abs_.endswith("DataReplication_Versao_1.md") and os.path.dirname(abs_) == PKG:
            pass
        return f"[{texto}]({href_de(pagina_dir, abs_)}{'#' + ancora if ancora else ''})"
    return re.sub(r"\[([^\]]*)\]\(([^)\s]+)\)", sub, md)

def mermaid_pre(codigo):
    return f'<div class="diagrama"><pre class="mermaid">{html.escape(codigo.strip())}</pre></div>'

def md_html(md, md_dir, pagina_dir):
    """Converte markdown em HTML, com links reescritos e blocos mermaid como <pre class="mermaid">."""
    md = reescrever_links(md, md_dir, pagina_dir)
    blocos = []
    def guarda(m):
        blocos.append(m.group(1))
        return f"\n\nMERMAIDBLOCO{len(blocos) - 1}FIM\n\n"
    md = re.sub(r"```mermaid\n(.*?)```", guarda, md, flags=re.S)
    h = markdown.markdown(md, extensions=["tables", "fenced_code", "sane_lists"])
    for k, b in enumerate(blocos):
        h = re.sub(rf"<p>MERMAIDBLOCO{k}FIM</p>", lambda _m, b=b: mermaid_pre(b), h)
    h = h.replace("<table>", '<div class="tabela"><table>').replace("</table>", "</table></div>")
    return h

def inline_md(txt, pagina_dir):
    """Markdown inline (código, negrito) e links automáticos para C/X/R/ADR."""
    h = markdown.markdown(txt)
    h = re.sub(r"^<p>(.*)</p>$", r"\1", h, flags=re.S)
    def link(m):
        ident = m.group(0)
        if ident.startswith("ADR-"):
            n = ident[4:]
            if n in ADR_ARQ:
                return f'<a href="{href_de(pagina_dir, ADR_ARQ[n])}">{ident}</a>'
            return ident
        if ident in PAGINAS:
            return f'<a href="{href_de(pagina_dir, os.path.join(OUT, PAGINAS[ident]["html"]))}">{ident}</a>'
        return ident
    partes = re.split(r"(<code>.*?</code>|<a [^>]*>.*?</a>)", h)
    for i in range(0, len(partes), 2):
        partes[i] = re.sub(r"\bADR-\d{3}\b|\b[CXR]\d{2}\b", link, partes[i])
    return "".join(partes)

def blocos_mermaid(path):
    return re.findall(r"```mermaid\n(.*?)```", ler(path), flags=re.S)

def d_arquivo(did):
    return glob.glob(os.path.join(PKG, "04_diagramas", f"{did}-*.md"))[0]

# ---------------------------------------------------------------- layout

CSS_REL = "assets/site.css"

def nav(pagina_dir, atual=None, atual_cat=None):
    r = lambda p: href_de(pagina_dir, os.path.join(OUT, p))
    out = [f'<nav class="sidebar" aria-label="Navegação">',
           f'<a class="marca" href="{r("index.html")}">DataReplication v1<span>Comitê técnico</span></a>',
           f'<a class="nav-inicio{" atual" if atual == "index" else ""}" href="{r("index.html")}">Resumo e requisitos</a>']
    for cat, nome, _ in CATS:
        ids = [i for i in sorted(PAGINAS) if PAGINAS[i]["cat"] == cat]
        aberto = " open" if cat == atual_cat else ""
        out.append(f'<details class="grupo"{aberto}><summary>{nome} <span class="qtd">{len(ids)}</span></summary><ul>')
        out.append(f'<li><a class="idx{" atual" if atual == cat else ""}" href="{r(cat + "/index.html")}">Índice da seção</a></li>')
        for i in ids:
            t = re.sub(r"^[CXR]\d{2}\s*—\s*", "", PAGINAS[i]["info"]["titulo"])
            cls = ' class="atual" aria-current="page"' if i == atual else ""
            out.append(f'<li><a{cls} href="{r(PAGINAS[i]["html"])}"><b>{i}</b> {html.escape(t)}</a></li>')
        out.append("</ul></details>")
    out.append("</nav>")
    return "\n".join(out)

def pagina(caminho_rel, titulo, corpo, fontes, atual=None, atual_cat=None, migalhas=None, mermaid=True):
    destino = os.path.join(OUT, caminho_rel)
    pdir = os.path.dirname(destino)
    os.makedirs(pdir, exist_ok=True)
    r = lambda p: href_de(pdir, os.path.join(OUT, p))
    fontes_txt = "\n".join("  " + os.path.relpath(f, PKG) for f in fontes)
    mig = ""
    if migalhas:
        mig = '<div class="migalhas">' + " / ".join(
            f'<a href="{r(h)}">{html.escape(t)}</a>' if h else html.escape(t) for t, h in migalhas) + "</div>"
    script = ""
    if mermaid:
        script = (f'<script src="{MERMAID_CDN}"></script>\n'
                  '<script>mermaid.initialize({startOnLoad: true, theme: "neutral", securityLevel: "strict", '
                  'flowchart: {htmlLabels: true, useMaxWidth: true}, sequence: {useMaxWidth: true}});</script>')
    doc = f"""<!doctype html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(titulo)} · DataReplication v1</title>
<link rel="stylesheet" href="{r(CSS_REL)}">
</head>
<body>
<!--
Fonte(s) usada(s) nesta página (rastreabilidade), relativas à raiz do pacote:
{fontes_txt}
Gerado em {HOJE} a partir do pacote DataReplication_Versao_1.
-->
<header class="topo">
<button class="menu" type="button" aria-label="Abrir navegação" onclick="document.body.classList.toggle('nav-aberta')">☰</button>
<a href="{r("index.html")}">DataReplication v1</a><span class="sep">·</span><span>Apresentação ao comitê técnico</span>
</header>
<div class="layout">
{nav(pdir, atual, atual_cat)}
<main>
{mig}
{corpo}
<footer class="rodape">
Gerado em {HOJE} a partir de <code>DataReplication_Versao_1/</code>. O conteúdo vem dos arquivos .md citados no código-fonte desta página;
textos marcados como <em>sugestão</em> são inferência do arquiteto e não decisões registradas.
</footer>
</main>
</div>
{script}
</body>
</html>
"""
    with open(destino, "w", encoding="utf-8") as f:
        f.write(doc)

# ---------------------------------------------------------------- página de cenário

ETAPAS_NOMES = ("Etapas", "Fluxo proposto")
PONTOS_ORIGEM = ("Falhas e tratativas", "Pontos em aberto")

def pagina_cenario(ident):
    d = PAGINAS[ident]
    info = d["info"]
    md_dir = os.path.dirname(d["md"])
    destino = os.path.join(OUT, d["html"])
    pdir = os.path.dirname(destino)
    conv = lambda m: md_html(m, md_dir, pdir)
    secs = info["secs"]
    usadas = set()
    fontes = [d["md"]]

    def sec(nome_prefixo):
        return [(n, c) for n, c in secs if n.startswith(nome_prefixo)]

    corpo = []
    titulo = info["titulo"]
    corpo.append(f'<h1>{html.escape(titulo)}</h1>')
    st = status_texto(info["status"])
    rel = relacionados(info["status"])
    meta = f'<div class="meta"><span class="selo {d["cat"]}">{CAT_NOME[d["cat"]]}</span>'
    if st:
        meta += f'<div><b>Status na fonte:</b> {conv(st).removeprefix("<p>").removesuffix("</p>")}</div>'
    if rel:
        meta += f'<div><b>Relacionados:</b> {conv(rel).removeprefix("<p>").removesuffix("</p>")}</div>'
    meta += "</div>"
    corpo.append(meta)

    # 1. Visão geral
    corpo.append('<section id="visao-geral"><h2>1. Visão geral do cenário</h2>')
    for n, c in sec("Cenário"):
        corpo.append(conv(c)); usadas.add(n)
    for n, c in sec("Decisão"):
        corpo.append(f'<div class="caixa decisao"><h3>Decisão registrada na fonte</h3>{conv(c)}</div>'); usadas.add(n)
    if d["cat"] == "parking-lot":
        corpo.append('<p class="aviso">Cenário <b>a rever</b>: o conteúdo abaixo traz propostas e riscos ainda '
                     '<b>não decididos na fonte</b>, salvo quando o texto diz o contrário.</p>')
    corpo.append("</section>")

    # 2. Diagrama
    corpo.append('<section id="diagrama"><h2>2. Diagrama</h2>')
    diags = [(n, c) for n, c in secs if n.startswith("Diagrama")]
    for n, c in diags:
        sub = n.split(":", 1)[1].strip() if ":" in n else ""
        if sub:
            corpo.append(f"<h3>{html.escape(sub[0].upper() + sub[1:])}</h3>")
        corpo.append(conv(c)); usadas.add(n)
    if not diags:
        blocos = blocos_mermaid(d["md"])
        for b in blocos:
            corpo.append(mermaid_pre(b))
        if not blocos:
            corpo.append('<p class="nd">A fonte não tem diagrama para este cenário.</p>')
    if ident in D_REL:
        did, k = D_REL[ident]
        dp = d_arquivo(did)
        fontes.append(dp)
        dt = ler(dp).split("\n")[0].lstrip("# ").strip()
        b = blocos_mermaid(dp)[k]
        corpo.append(f'<h3>Arquitetura geral relacionada: <a href="{href_de(pdir, dp)}">{html.escape(dt)}</a></h3>'
                     '<p class="nota">Diagrama de <code>04_diagramas/</code> escolhido pelo site como contexto; '
                     'o cenário fonte não faz essa associação.</p>')
        corpo.append(mermaid_pre(b))
    corpo.append("</section>")

    # 3. Etapas
    corpo.append('<section id="etapas"><h2>3. Legenda e etapas</h2>')
    etapas = [(n, c) for n, c in secs if n.startswith(ETAPAS_NOMES) or n.startswith("Checklist")]
    for n, c in etapas:
        if n.startswith("Fluxo proposto"):
            corpo.append('<p class="nota">A fonte chama esta seção de <b>Fluxo proposto</b>: é proposta, não decisão fechada.</p>')
        elif n.startswith("Checklist"):
            corpo.append(f'<h3>{html.escape(n)}</h3>')
        corpo.append(f'<div class="etapas">{conv(c)}</div>'); usadas.add(n)
    if not etapas:
        corpo.append('<p class="nd">A fonte não tem seção de etapas para este cenário; o conteúdo está organizado '
                     'por hipóteses, reproduzidas no anexo ao final da página.</p>')
    corpo.append("</section>")

    # 4. Pontos a avaliar
    corpo.append('<section id="pontos"><h2>4. Pontos que precisam ser avaliados</h2>')
    corpo.append('<p class="nota">Reescritos a partir das seções “Falhas e tratativas” e “Pontos em aberto” '
                 '(e de outras seções de risco, quando existem). Em cada ponto, <b>O que a fonte define</b> vem da '
                 'documentação; <b>Sugestão de encaminhamento</b> é <b>inferência do arquiteto</b>, não decisão registrada.</p>')
    pj = os.path.join(PONTOS, f"{ident}.json")
    pontos = json.load(open(pj, encoding="utf-8"))["pontos"] if os.path.exists(pj) else None
    if pontos is None:
        corpo.append('<p class="nd">Pontos ainda não redigidos.</p>')
    elif not pontos:
        corpo.append('<p class="nd">A fonte não lista falhas nem pontos em aberto para este cenário.</p>')
    for k, p in enumerate(pontos or [], 1):
        nf = p["na_fonte"]
        cls_nf = " nd-fonte" if nf.startswith("Não decidido na fonte") else ""
        corpo.append(f'''<article class="ponto">
<h3><span class="num">{k}</span>{inline_md(p["titulo"], pdir)} <span class="origem">{html.escape(p["origem"])}</span></h3>
<dl>
<dt>Contexto</dt><dd>{inline_md(p["contexto"], pdir)}</dd>
<dt>O que a fonte define</dt><dd class="{cls_nf.strip()}">{inline_md(nf, pdir)}</dd>
<dt class="sug">Sugestão de encaminhamento <small>(inferência, não decidida)</small></dt><dd class="sug">{inline_md(p["sugestao"], pdir)}</dd>
</dl>
</article>''')
    corpo.append("</section>")

    # 5. ADRs
    corpo.append('<section id="adrs"><h2>5. ADRs relacionados</h2>')
    ids = adrs_do_cabecalho(info["status"])
    if ids:
        corpo.append('<div class="tabela"><table><thead><tr><th>ADR</th><th>Título</th><th>Decisão resumida</th><th>Arquivo</th></tr></thead><tbody>')
        for n in ids:
            if n not in ADR_ARQ:
                corpo.append(f'<tr><td>ADR-{n}</td><td colspan="3" class="nd">Citado na fonte, mas sem arquivo em 03_adr/.</td></tr>')
                continue
            href = href_de(pdir, ADR_ARQ[n])
            corpo.append(f'<tr><td class="nw"><a href="{href}">ADR-{n}</a></td><td>{inline_md(html.escape(ADR_TIT[n], quote=False), pdir)}</td>'
                         f'<td>{inline_md(RESUMO.get(n, "Não resumido."), pdir)}</td>'
                         f'<td><a href="{href}">{html.escape(os.path.basename(ADR_ARQ[n]))}</a></td></tr>')
        corpo.append("</tbody></table></div>")
        corpo.append('<p class="nota">Lista extraída do cabeçalho “ADRs” do arquivo fonte. A decisão resumida condensa a seção “Decisão” de cada ADR.</p>')
    else:
        corpo.append('<p class="nd">O cabeçalho do arquivo fonte não lista ADRs.</p>')
    corpo.append("</section>")

    # Anexo: texto original
    resto = [(n, c) for n, c in secs if n not in usadas]
    if resto:
        corpo.append('<section id="anexo"><h2>Anexo: texto original da fonte</h2>'
                     '<p class="nota">Seções do .md fonte reproduzidas sem reescrita, para conferência.</p>')
        for n, c in resto:
            corpo.append(f'<details class="anexo"><summary>{html.escape(n)}</summary>{conv(c)}</details>')
        corpo.append("</section>")

    mig = [("Início", "index.html"), (CAT_NOME[d["cat"]], d["cat"] + "/index.html"), (ident, None)]
    pagina(d["html"], titulo, "\n".join(corpo), fontes, atual=ident, atual_cat=d["cat"], migalhas=mig)

# ---------------------------------------------------------------- índices

def pagina_indice_cat(cat, nome, desc):
    ids = [i for i in sorted(PAGINAS) if PAGINAS[i]["cat"] == cat]
    pdir = os.path.join(OUT, cat)
    linhas = []
    for i in ids:
        info = PAGINAS[i]["info"]
        t = re.sub(r"^[CXR]\d{2}\s*—\s*", "", info["titulo"])
        resumo = ""
        for n, c in info["secs"]:
            if n.startswith("Cenário"):
                par = c.strip().split("\n\n")[0]
                resumo = inline_md(reescrever_links(par, os.path.dirname(PAGINAS[i]["md"]), pdir), pdir)
                resumo = re.sub(r"<a [^>]*>(.*?)</a>", r"\1", resumo)
                break
        st = status_texto(info["status"])
        st = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", st)
        linhas.append(f'<a class="cartao" href="{href_de(pdir, os.path.join(OUT, PAGINAS[i]["html"]))}">'
                      f'<span class="id">{i}</span><span class="t">{html.escape(t)}</span>'
                      f'<span class="st">{inline_md(st, pdir)}</span><span class="r">{resumo}</span></a>')
    extra = ""
    if cat in ("success-path", "fail-path"):
        extra = ('<p class="nota">Classificação success/fail aprovada pelo Bruno em 13/10/2026. '
                 'Fonte: <code>02_cenarios/atualizados/</code>.</p>')
    corpo = f'<h1>{nome}</h1><p class="lead">{desc}</p>{extra}<div class="cartoes">{"".join(linhas)}</div>'
    fontes = [PAGINAS[i]["md"] for i in ids]
    pagina(f"{cat}/index.html", nome, corpo, fontes, atual=cat, atual_cat=cat,
           migalhas=[("Início", "index.html"), (nome, None)], mermaid=False)

def secao_central(txt, numero):
    m = re.search(rf"^## {numero}\. .*?$(.*?)(?=^## |\Z)", txt, flags=re.M | re.S)
    return m.group(1).strip()

def pagina_inicio():
    central = os.path.join(PKG, "DataReplication_Versao_1.md")
    txt = ler(central)
    conv = lambda m: md_html(m, PKG, OUT)
    s1, s2, s3 = secao_central(txt, 1), secao_central(txt, 2), secao_central(txt, 3)
    s10, s11 = secao_central(txt, 10), secao_central(txt, 11)
    fora = re.search(r"^> \*\*Não é objetivo da v1:\*\*(.*)$", s1, flags=re.M).group(1).strip()
    s1_sem_fora = re.sub(r"^> \*\*Não é objetivo da v1:\*\*.*$", "", s1, flags=re.M)
    estado = re.search(r"^\*\*Estado:\*\*(.*?)·", txt, flags=re.M).group(1).strip()

    cont = {c[0]: sum(1 for d in PAGINAS.values() if d["cat"] == c[0]) for c in CATS}
    cartoes = "".join(
        f'<a class="cartao secao {c}" href="{c}/index.html"><span class="t">{n}</span>'
        f'<span class="st">{cont[c]} páginas</span><span class="r">{d}</span></a>'
        for c, n, d in CATS)

    # Capabilities: requisitos em forma de quadro (dados extraídos da seção 3 e da seção 1)
    corpo = f"""
<h1>DataReplication v1</h1>
<p class="lead">Camada intermediária entre a aplicação e o banco que só confirma a escrita depois de o dado estar
durável na AWS (DynamoDB) e na Azure (Cosmos DB). Estado da documentação: {html.escape(estado)}.</p>
<div class="kpis">
<div><b>RPO = 0</b><span>em operação normal</span></div>
<div><b>RTO = Delta-T</b><span>sem número: não decidido na fonte</span></div>
<div><b>~3.000 TPS</b><span>PutItem e BatchWriteItem</span></div>
<div><b>P99 ≤ 50 ms</b><span>overhead, sem a escrita no banco</span></div>
</div>
<nav class="cartoes secoes" aria-label="Seções do site">{cartoes}</nav>

<section id="resumo"><h2>Resumo executivo</h2>
<h3>O problema</h3>
{conv(s1_sem_fora)}
<h3>A solução em uma página</h3>
{conv(s2)}
</section>

<section id="requisitos"><h2>Requisitos e capabilities</h2>
<div class="duas">
<div class="caixa dentro"><h3>Dentro da v1</h3>
<ul>
<li>ACK só depois do gate: commit do Raft na maioria AWS e índice durável na maioria Azure.</li>
<li>API compatível com DynamoDB: <code>PutItem</code>, <code>DeleteItem</code>, <code>BatchWriteItem</code>, <code>GetItem</code>, <code>BatchGetItem</code>, <code>Query</code> e <code>Scan</code>.</li>
<li>SDKs AWS Java 2.x, Go v2 e .NET v4.</li>
<li>Um único modo, strong consistency: leitura sempre forte na tabela base; Query e Scan em GSI recusadas.</li>
<li>Modo degradado por política da instalação (<code>DEGRADED_POLICY</code> accept com lease D, ou reject).</li>
<li>Uma instalação por cliente; autorização por aplicação com mTLS.</li>
</ul>
<p class="nota">Itens condensados das seções 2 e 3 do documento principal.</p></div>
<div class="caixa fora"><h3>Fora da v1</h3>
{conv(fora)}
<p class="nota">Texto do documento principal, seção 1 (“Não é objetivo da v1”). Valores de D, M e do RTO: <b>não decididos na fonte</b>.</p></div>
</div>
<h3>RPO, RTO e metas</h3>
{conv(re.sub(r"^### ", "#### ", s3, flags=re.M))}
</section>

<section id="pendencias"><h2>Pendências e riscos em aberto</h2>
{conv(s10)}
</section>

<section id="pronto"><h2>Definição de pronto da v1</h2>
<details class="anexo"><summary>Lista do documento principal (a provar, não feita)</summary>
{conv(s11)}
</details>
</section>
"""
    pagina("index.html", "Resumo executivo e requisitos", corpo, [central], atual="index",
           migalhas=None, mermaid=True)

CSS = r"""
:root{--bg:#f7f7f5;--papel:#fff;--tx:#1e2328;--tx2:#5b636b;--linha:#e2e4e6;--acento:#1f5fa8;--acento2:#e9f1fb;
--ok:#1e7a46;--okbg:#e8f5ee;--falha:#b4441c;--falhabg:#fcefe8;--cx:#6a3fa0;--cxbg:#f2ecfa;--pk:#8a6d00;--pkbg:#fbf5dc;
--sug:#0f6b6b;--sugbg:#e8f6f6;color-scheme:light}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;background:var(--bg);color:var(--tx);font:16px/1.55 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
a{color:var(--acento)}
code{font:0.88em ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;background:#eef0f2;padding:1px 4px;border-radius:4px;overflow-wrap:anywhere}
.topo{position:sticky;top:0;z-index:20;display:flex;gap:8px;align-items:center;height:48px;padding:0 16px;background:#14202e;color:#cfd8e3;font-size:14px}
.topo a{color:#fff;font-weight:600;text-decoration:none}.topo .sep{opacity:.5}
.menu{display:none;background:none;border:0;color:#fff;font-size:20px;cursor:pointer;padding:4px 8px}
.layout{display:grid;grid-template-columns:300px minmax(0,1fr);min-height:calc(100vh - 48px)}
.sidebar{position:sticky;top:48px;height:calc(100vh - 48px);overflow:auto;background:var(--papel);border-right:1px solid var(--linha);padding:16px 12px;font-size:14px}
.marca{display:block;font-weight:700;color:var(--tx);text-decoration:none;margin:0 6px 12px}.marca span{display:block;font-weight:400;color:var(--tx2);font-size:12px}
.nav-inicio{display:block;padding:6px;border-radius:6px;text-decoration:none;color:var(--tx);font-weight:600}
.grupo{margin-top:6px}.grupo summary{cursor:pointer;padding:6px;font-weight:600;border-radius:6px}
.grupo summary:hover,.nav-inicio:hover{background:var(--acento2)}
.qtd{font-weight:400;color:var(--tx2);font-size:12px}
.grupo ul{list-style:none;margin:0;padding:0 0 0 6px}
.grupo li a{display:block;padding:4px 6px;border-radius:6px;color:var(--tx);text-decoration:none;line-height:1.35}
.grupo li a:hover{background:var(--acento2)}.grupo li a b{color:var(--tx2);font-weight:600;margin-right:4px}
.grupo li a.idx{color:var(--tx2);font-style:italic}
a.atual{background:var(--acento2);color:var(--acento)!important;font-weight:600}
main{padding:24px 40px 40px;max-width:1100px;width:100%}
h1{font-size:28px;line-height:1.25;margin:4px 0 12px}
h2{font-size:21px;margin:36px 0 12px;padding-bottom:6px;border-bottom:2px solid var(--linha)}
h3{font-size:17px;margin:22px 0 8px}h4{font-size:15px;margin:18px 0 6px}
.lead{font-size:18px;color:var(--tx2);max-width:75ch}
.migalhas{font-size:13px;color:var(--tx2);margin-bottom:8px}
.meta{background:var(--papel);border:1px solid var(--linha);border-radius:10px;padding:12px 14px;font-size:14px;display:grid;gap:6px}
.meta p{margin:0}
.selo{justify-self:start;font-size:12px;font-weight:700;padding:2px 10px;border-radius:99px;text-transform:uppercase;letter-spacing:.03em}
.selo.success-path{background:var(--okbg);color:var(--ok)}.selo.fail-path{background:var(--falhabg);color:var(--falha)}
.selo.complex-path{background:var(--cxbg);color:var(--cx)}.selo.parking-lot{background:var(--pkbg);color:var(--pk)}
.diagrama{background:var(--papel);border:1px solid var(--linha);border-radius:10px;padding:12px;margin:12px 0;overflow-x:auto}
pre.mermaid{margin:0;background:none;text-align:center;font-family:inherit;white-space:pre}
pre.mermaid[data-processed]{white-space:normal}
.tabela{overflow-x:auto;margin:12px 0}
table{border-collapse:collapse;width:100%;background:var(--papel);font-size:14px}
th,td{border:1px solid var(--linha);padding:7px 10px;text-align:left;vertical-align:top}
th{background:#f0f2f4}td.nw{white-space:nowrap}
.etapas ol>li{margin-bottom:6px}
.nota{font-size:13px;color:var(--tx2)}
.nd{color:var(--pk);background:var(--pkbg);padding:8px 12px;border-radius:8px;font-size:14px}
.aviso{background:var(--pkbg);border-left:4px solid var(--pk);padding:8px 12px;border-radius:6px}
.caixa{background:var(--papel);border:1px solid var(--linha);border-radius:10px;padding:4px 16px 10px;margin:14px 0}
.caixa.decisao{border-left:4px solid var(--acento)}
.caixa.dentro{border-top:4px solid var(--ok)}.caixa.fora{border-top:4px solid var(--falha)}
.duas{display:grid;grid-template-columns:1fr 1fr;gap:16px}
.ponto{background:var(--papel);border:1px solid var(--linha);border-radius:10px;padding:10px 16px;margin:12px 0}
.ponto h3{margin:6px 0 8px;display:flex;gap:8px;align-items:baseline;flex-wrap:wrap}
.ponto .num{display:inline-grid;place-items:center;min-width:24px;height:24px;border-radius:50%;background:var(--acento);color:#fff;font-size:13px}
.origem{font-size:11px;font-weight:600;color:var(--tx2);background:#eef0f2;border-radius:99px;padding:2px 8px}
.ponto dl{margin:0}.ponto dt{font-weight:700;font-size:13px;text-transform:uppercase;letter-spacing:.03em;color:var(--tx2);margin-top:8px}
.ponto dd{margin:2px 0 0}.ponto dd.nd-fonte{color:var(--pk)}
.ponto dt.sug{color:var(--sug)}.ponto dt.sug small{text-transform:none;letter-spacing:0;font-weight:400}
.ponto dd.sug{background:var(--sugbg);border-left:3px solid var(--sug);padding:6px 10px;border-radius:6px}
details.anexo{background:var(--papel);border:1px solid var(--linha);border-radius:8px;padding:6px 14px;margin:8px 0}
details.anexo summary{cursor:pointer;font-weight:600}
.kpis{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin:18px 0}
.kpis div{background:var(--papel);border:1px solid var(--linha);border-radius:10px;padding:12px}
.kpis b{display:block;font-size:20px}.kpis span{font-size:13px;color:var(--tx2)}
.cartoes{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:12px;margin:16px 0}
.cartao{display:flex;flex-direction:column;gap:4px;background:var(--papel);border:1px solid var(--linha);border-radius:10px;padding:12px 14px;color:var(--tx);text-decoration:none}
.cartao:hover{border-color:var(--acento);box-shadow:0 2px 8px rgba(0,0,0,.06)}
.cartao .id{font-size:12px;font-weight:700;color:var(--tx2)}.cartao .t{font-weight:700}
.cartao .st{font-size:12px;color:var(--tx2)}.cartao .r{font-size:14px;color:var(--tx2);display:-webkit-box;-webkit-line-clamp:4;-webkit-box-orient:vertical;overflow:hidden}
.cartao.secao{border-top:4px solid var(--linha)}
.cartao.success-path{border-top-color:var(--ok)}.cartao.fail-path{border-top-color:var(--falha)}
.cartao.complex-path{border-top-color:var(--cx)}.cartao.parking-lot{border-top-color:var(--pk)}
blockquote{margin:12px 0;padding:6px 14px;border-left:4px solid var(--linha);color:var(--tx2)}
.rodape{margin-top:48px;padding-top:12px;border-top:1px solid var(--linha);font-size:12px;color:var(--tx2)}
@media (max-width:900px){
 .layout{grid-template-columns:1fr}
 .menu{display:block}
 .sidebar{display:none;position:fixed;top:48px;left:0;right:0;bottom:0;height:auto;z-index:15}
 body.nav-aberta .sidebar{display:block}
 main{padding:16px}
 .duas,.kpis{grid-template-columns:1fr 1fr}
}
@media (max-width:560px){.duas,.kpis{grid-template-columns:1fr}h1{font-size:23px}}
@media print{.sidebar,.topo{display:none}.layout{display:block}main{max-width:none}details{display:block}}
"""

def main():
    os.makedirs(os.path.join(OUT, "assets"), exist_ok=True)
    with open(os.path.join(OUT, CSS_REL), "w", encoding="utf-8") as f:
        f.write(CSS.strip() + "\n")
    pagina_inicio()
    for c, n, d in CATS:
        pagina_indice_cat(c, n, d)
    for i in sorted(PAGINAS):
        pagina_cenario(i)
    print(f"{len(PAGINAS)} cenários gerados em {OUT}")

if __name__ == "__main__":
    main()

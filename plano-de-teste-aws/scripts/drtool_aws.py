"""Auxiliar dos cenários da sessão na AWS (plano-de-teste-aws/Taskfile.yaml). Só biblioteca padrão.

Fala com as células pelos port-forwards do infra-aws/scripts/ponte.sh (task conectar):
  localhost:1909N -> cell-N-0:9090 (/status, /metrics, /admin/reconstruir)
  localhost:1800N -> cell-N-0:8000 (API DynamoDB da camada)
e lê as tabelas direto no DynamoDB com a sua credencial (aws CLI), como o ORC-03 pede.

Subcomandos:
  ping                                  confere que os 3 túneis de administração respondem
  status                                tabela com papel, termo, commit, gate, aplicado e estado
  leader                                imprime N da líder (sai 1 sem líder)
  follower [N]                          imprime uma follower (a primeira, ou a de índice N)
  campo CELL EXPR                       avalia EXPR com d = /status da célula (ex.: 'd["match"]')
  escrever CELL COUNT PREFIXO SAIDA     grava COUNT itens pela célula; salva o resultado em SAIDA
  metrica CELL NOME                     soma das séries NOME no /metrics da célula
  esperar-lider TIMEOUT [EXCETO]        espera uma líder (diferente de EXCETO); imprime N
  esperar-alcance CELL TIMEOUT [TOL]    espera a célula em normal e com atraso <= TOL da líder
  esperar-estados CELL TIMEOUT ESTADOS  espera a célula passar pelos estados e voltar a normal
  quiescer TIMEOUT                      espera as 3 com aplicado >= commit da líder por 10 s
  comparar [PREFIXO]                    R4: mesmas pk e mesmo _dr_v nas 3 tabelas (direto no DynamoDB)
  oraculo LEDGER... [--prefixo P]       R1 a R4 do ORC-04 sobre ledgers do k6 (.json/.json.gz) ou do escrever
  ler CELL PK                           GetItem forte pela célula (mostra o que o cliente veria)
  contar CELL                           quantidade de itens na tabela da célula (direto no DynamoDB)
  apagar-tabela CELL                    apaga TODOS os itens da tabela da célula direto no DynamoDB
  reconstruir CELL                      POST /admin/reconstruir (202 na follower, 409 na líder)
  anotar TEXTO                          anotação no Grafana local (localhost:3001); ignora erro
"""
import concurrent.futures as cf
import gzip
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

CELLS = (1, 2, 3)
REGIAO = os.environ.get("REGIAO", "sa-east-1")
GRAFANA = os.environ.get("GRAFANA_URL", "http://localhost:3001")
os.environ.setdefault("AWS_PAGER", "")


def tabela(n):
    return f"cadastro_cliente_cell_{n}"


def agora():
    return time.strftime("%H:%M:%S")


# ---------- células ----------

def status(n, timeout=2):
    with urllib.request.urlopen(f"http://localhost:1909{n}/status", timeout=timeout) as r:
        return json.load(r)


def try_status(n):
    try:
        return status(n)
    except Exception:
        return None


def leader(exceto=None):
    for n in CELLS:
        if n == exceto:
            continue
        d = try_status(n)
        if d and d.get("papel") == "lider":
            return n
    return None


def metrica(n, nome):
    with urllib.request.urlopen(f"http://localhost:1909{n}/metrics", timeout=3) as r:
        texto = r.read().decode()
    total = 0.0
    for linha in texto.splitlines():
        if linha.startswith(nome + " ") or linha.startswith(nome + "{"):
            total += float(linha.rsplit(" ", 1)[1])
    return total


def put(cell, pk):
    corpo = json.dumps({"TableName": tabela(cell), "Item": {
        "pk": {"S": pk}, "nome": {"S": "Maria"}, "cpf": {"S": "52998224725"}}}).encode()
    req = urllib.request.Request(f"http://localhost:1800{cell}", data=corpo, headers={
        "Content-Type": "application/x-amz-json-1.0", "X-Amz-Target": "DynamoDB_20120810.PutItem"})
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, r.headers.get("x-dr-index", ""), r.headers.get("x-dr-code", "")
    except urllib.error.HTTPError as e:
        return e.code, e.headers.get("x-dr-index", ""), e.headers.get("x-dr-code", "")
    except Exception as e:
        return 0, "", type(e).__name__


def get_item(cell, pk):
    corpo = json.dumps({"TableName": tabela(cell), "Key": {"pk": {"S": pk}}, "ConsistentRead": True}).encode()
    req = urllib.request.Request(f"http://localhost:1800{cell}", data=corpo, headers={
        "Content-Type": "application/x-amz-json-1.0", "X-Amz-Target": "DynamoDB_20120810.GetItem"})
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def escrever(cell, count, prefixo, saida):
    registros = []
    for i in range(count):
        pk = f"{prefixo}{int(time.time() * 1000)}-{i}"
        st, idx, code = put(cell, pk)
        registros.append({"pk": pk, "celula": str(cell), "status": str(st), "dr_index": idx, "dr_code": code})
        print(f"  {pk}  HTTP {st}  x-dr-index={idx or '-'}{'  x-dr-code=' + code if code else ''}")
    os.makedirs(os.path.dirname(saida) or ".", exist_ok=True)
    with open(saida, "w") as f:
        for r in registros:
            f.write(json.dumps(r) + "\n")
    ok = sum(1 for r in registros if r["status"] == "200")
    print(f"{ok}/{count} confirmadas (HTTP 200); resultado em {saida}")
    return ok == count


def print_status():
    print(f"{'célula':<7}{'papel':<10}{'líder':>6}{'termo':>7}{'commit':>10}{'gate':>10}{'aplicado':>10}  estado")
    for n in CELLS:
        d = try_status(n)
        if not d:
            print(f"cell-{n:<2} (sem resposta: pod fora ou túnel caído)")
            continue
        print(f"cell-{n:<2} {d.get('papel', '?'):<10}{d.get('lider', 0):>6}{d.get('termo', 0):>7}"
              f"{d.get('commit', 0):>10}{d.get('gate', 0):>10}{d.get('aplicado', 0):>10}  "
              f"{d.get('modo', {}).get('estado', '?')}{'  erroApply=' + d['erroApply'] if d.get('erroApply') else ''}")


def esperar_lider(timeout, exceto=None):
    t0 = time.time()
    while time.time() - t0 < timeout:
        n = leader(exceto)
        if n:
            print(f"{agora()} líder: cell-{n} ({time.time() - t0:.1f} s)", file=sys.stderr)
            return n
        time.sleep(0.2)
    sys.exit(f"FAIL: sem líder em {timeout} s")


def esperar_alcance(cell, timeout, tol):
    """Alcançou = modo normal e aplicado da célula a até TOL entradas do commit da líder, 3 leituras seguidas."""
    t0 = time.time()
    seguidas = 0
    ultimo = 0
    while time.time() - t0 < timeout:
        lid = leader()
        f = try_status(cell)
        l = try_status(lid) if lid else None
        if f and l:
            atraso = max(0, l["commit"] - f["aplicado"])
            estado = f.get("modo", {}).get("estado", "?")
            if time.time() - ultimo >= 5:
                print(f"{agora()} cell-{cell}: estado={estado} aplicado={f['aplicado']} "
                      f"commit da líder cell-{lid}={l['commit']} atraso={atraso}")
                ultimo = time.time()
            seguidas = seguidas + 1 if (estado == "normal" and atraso <= tol) else 0
            if seguidas >= 3:
                print(f"{agora()} PASS: cell-{cell} alcançou a líder em {time.time() - t0:.1f} s (atraso {atraso} <= {tol})")
                return True
        time.sleep(1)
    sys.exit(f"FAIL: cell-{cell} não alcançou a líder em {timeout} s")


def esperar_estados(cell, timeout, estados):
    """Registra cada mudança de modo.estado e termina quando voltou a normal depois de ver um dos ESTADOS."""
    t0 = time.time()
    vistos = []
    anterior = None
    while time.time() - t0 < timeout:
        d = try_status(cell)
        estado = d.get("modo", {}).get("estado", "?") if d else "(sem resposta)"
        if estado != anterior:
            extra = ""
            if d and d.get("reconstrucao"):
                r = d["reconstrucao"]
                extra = f" fonte=cell-{r.get('fonte')} copiados={r.get('itensCopiados')}"
            print(f"{agora()} cell-{cell}: {estado}{extra} (+{time.time() - t0:.0f} s)")
            vistos.append(estado)
            anterior = estado
        if estado == "normal" and any(e in vistos for e in estados):
            print(f"{agora()} PASS: cell-{cell} passou por {' -> '.join(vistos)}")
            return True
        time.sleep(0.5)
    sys.exit(f"FAIL: cell-{cell} não voltou a normal em {timeout} s (vistos: {vistos})")


def quiescer(timeout):
    t0 = time.time()
    desde = None
    while time.time() - t0 < timeout:
        lid = leader()
        ds = {n: try_status(n) for n in CELLS}
        if lid and all(ds.values()):
            commit = ds[lid]["commit"]
            if all(d["aplicado"] >= commit for d in ds.values()):
                desde = desde or time.time()
                if time.time() - desde >= 10:
                    print(f"{agora()} quiescente: as 3 com aplicado >= {commit}")
                    return True
            else:
                desde = None
        time.sleep(1)
    sys.exit(f"FAIL: as células não ficaram quiescentes em {timeout} s")


# ---------- DynamoDB direto ----------

def aws(*args):
    r = subprocess.run(["aws", "--region", REGIAO, *args, "--output", "json"], capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"FAIL: aws {' '.join(args[:2])}: {r.stderr.strip()}")
    return json.loads(r.stdout) if r.stdout.strip() else {}


def scan_direto(n, prefixo=None):
    args = ["dynamodb", "scan", "--table-name", tabela(n), "--consistent-read",
            "--projection-expression", "pk, #v", "--expression-attribute-names", json.dumps({"#v": "_dr_v"})]
    if prefixo:
        args += ["--filter-expression", "begins_with(pk, :p)",
                 "--expression-attribute-values", json.dumps({":p": {"S": prefixo}})]
    itens = {}
    for it in aws(*args).get("Items", []):
        v = it.get("_dr_v", {})
        itens[it["pk"]["S"]] = int(v.get("N") or v.get("S") or 0)
    return itens


def scans(prefixo=None):
    with cf.ThreadPoolExecutor(3) as ex:
        return dict(zip(CELLS, ex.map(lambda n: scan_direto(n, prefixo), CELLS)))


def comparar(prefixo=None, tabelas=None):
    tabelas = tabelas or scans(prefixo)
    alvo = f" (pk com prefixo {prefixo})" if prefixo else ""
    for n in CELLS:
        print(f"  {tabela(n)}: {len(tabelas[n])} itens{alvo}")
    todas = set().union(*tabelas.values())
    divergentes = [pk for pk in todas if len({tabelas[n].get(pk) for n in CELLS}) != 1]
    if divergentes:
        print(f"R4 FALHOU: {len(divergentes)} pk ausentes ou com _dr_v diferente. Exemplos:")
        for pk in sorted(divergentes)[:5]:
            print(f"  {pk}: " + ", ".join(f"cell-{n}={tabelas[n].get(pk, 'ausente')}" for n in CELLS))
        return False
    print(f"R4 OK: as 3 tabelas têm as mesmas {len(todas)} pk com o mesmo _dr_v")
    return True


def ler_ledger(caminho):
    abrir = gzip.open if caminho.endswith(".gz") else open
    with abrir(caminho, "rt") as f:
        for linha in f:
            try:
                d = json.loads(linha)
            except ValueError:
                continue
            if d.get("metric") == "dr_ledger" and d.get("type") == "Point":
                # pk, dr_index e dr_code vêm nos metadados (carga.js); o resto nas tags.
                yield {**d["data"].get("tags", {}), **(d["data"].get("metadata") or {})}
            elif "pk" in d and "status" in d:  # saída do subcomando escrever
                yield d


def classe(t):
    """ORC-02: CONFIRMADA, RECUSADA_SEGURA (nada entrou no log) ou INCERTA."""
    st, code = t.get("status", "0"), t.get("dr_code", "")
    if st == "200":
        return "CONFIRMADA"
    if st.startswith("4") or (st == "503" and code in ("NO_LEADER", "THROTTLING", "CELL_RECOVERING")):
        return "RECUSADA_SEGURA"
    return "INCERTA"


def oraculo(ledgers, prefixo=None):
    reqs = {}
    for caminho in ledgers:
        for t in ler_ledger(caminho):
            # Repetições da mesma pk (não acontecem no carga.js) ficam com a melhor classe.
            c = classe(t)
            ant = reqs.get(t["pk"])
            if not ant or c == "CONFIRMADA":
                reqs[t["pk"]] = (c, int(t.get("dr_index") or 0))
    if not reqs:
        sys.exit("FAIL: nenhum registro no ledger")
    if not prefixo:
        # Todas as pk do carga.js começam com cli#<CENARIO>-: o prefixo comum limita o scan.
        prefixo = os.path.commonprefix(list(reqs))
    tabelas = scans(prefixo)
    contagem = {"CONFIRMADA": 0, "RECUSADA_SEGURA": 0, "INCERTA": 0}
    falhas = {"R1": [], "R2": [], "R3": []}
    incertas_presentes = 0
    for pk, (c, idx) in reqs.items():
        contagem[c] += 1
        em = [n for n in CELLS if pk in tabelas[n]]
        if c == "CONFIRMADA" and (len(em) != 3 or any(tabelas[n][pk] < idx for n in em)):
            falhas["R1"].append(pk)
        elif c == "RECUSADA_SEGURA" and em:
            falhas["R2"].append(pk)
        elif c == "INCERTA":
            if len(em) not in (0, 3):
                falhas["R3"].append(pk)
            incertas_presentes += len(em) == 3
    print(f"ledger: {len(reqs)} requisições, prefixo {prefixo!r}")
    print(f"  CONFIRMADA={contagem['CONFIRMADA']}  RECUSADA_SEGURA={contagem['RECUSADA_SEGURA']}  "
          f"INCERTA={contagem['INCERTA']} ({incertas_presentes} gravadas nas 3, o resto em nenhuma)")
    regras = {"R1": "toda CONFIRMADA nas 3 tabelas com _dr_v >= x-dr-index",
              "R2": "nenhuma RECUSADA_SEGURA em tabela alguma",
              "R3": "cada INCERTA nas 3 ou em nenhuma"}
    ok = True
    for r, desc in regras.items():
        if falhas[r]:
            ok = False
            print(f"{r} FALHOU ({desc}): {len(falhas[r])} pk. Exemplos: {', '.join(sorted(falhas[r])[:3])}")
        else:
            print(f"{r} OK: {desc}")
    ok = comparar(prefixo, tabelas) and ok
    print("RPO = 0: PASS" if ok else "RPO = 0: FAIL")
    return ok


def contar(n):
    return aws("dynamodb", "scan", "--table-name", tabela(n), "--select", "COUNT").get("Count", 0)


def apagar_tabela(n):
    chaves = list(scan_direto(n))
    print(f"{tabela(n)}: apagando {len(chaves)} itens direto no DynamoDB (sem passar pela camada)")

    def lote(pks):
        req = {tabela(n): [{"DeleteRequest": {"Key": {"pk": {"S": pk}}}} for pk in pks]}
        for _ in range(8):  # UnprocessedItems: repete o que sobrou
            r = aws("dynamodb", "batch-write-item", "--request-items", json.dumps(req))
            req = r.get("UnprocessedItems") or {}
            if not req:
                return
            time.sleep(0.5)

    lotes = [chaves[i:i + 25] for i in range(0, len(chaves), 25)]
    with cf.ThreadPoolExecutor(16) as ex:
        for i, _ in enumerate(ex.map(lote, lotes), 1):
            if i % 200 == 0:
                print(f"  {i * 25}/{len(chaves)}")
    print(f"{tabela(n)}: {contar(n)} itens restantes (itens novos chegam pelo apply enquanto a carga roda)")


def reconstruir(n):
    req = urllib.request.Request(f"http://localhost:1909{n}/admin/reconstruir", data=b"", method="POST")
    try:
        with urllib.request.urlopen(req, timeout=5) as r:
            print(f"HTTP {r.status} {r.read().decode().strip()}")
            return r.status == 202
    except urllib.error.HTTPError as e:
        print(f"HTTP {e.code} {e.read().decode().strip()}")
        return False


def anotar(texto):
    corpo = json.dumps({"time": int(time.time() * 1000), "tags": ["cenario"], "text": texto}).encode()
    req = urllib.request.Request(f"{GRAFANA}/api/annotations", data=corpo,
                                 headers={"Content-Type": "application/json"})
    try:
        urllib.request.urlopen(req, timeout=2)
    except Exception:
        pass
    print(f"{agora()} >>> {texto}")


def main(argv):
    cmd, args = argv[0], argv[1:]
    if cmd == "ping":
        fora = [n for n in CELLS if not try_status(n)]
        if fora:
            sys.exit(f"FAIL: sem resposta de {', '.join(f'localhost:1909{n}' for n in fora)}. "
                     "Rode 'task conectar' em outro terminal e deixe aberto.")
        print("túneis OK")
    elif cmd == "status":
        print_status()
    elif cmd == "leader":
        n = leader()
        if not n:
            sys.exit(1)
        print(n)
    elif cmd == "follower":
        lid = leader() or sys.exit("FAIL: sem líder")
        fs = [n for n in CELLS if n != lid]
        print(fs[int(args[0]) if args else 0])
    elif cmd == "escrever":
        sys.exit(0 if escrever(int(args[0]), int(args[1]), args[2], args[3]) else 1)
    elif cmd == "campo":
        d = try_status(int(args[0])) or sys.exit(1)
        print(eval(args[1], {}, {"d": d}))
    elif cmd == "metrica":
        print(int(metrica(int(args[0]), args[1])))
    elif cmd == "esperar-lider":
        print(esperar_lider(float(args[0]), int(args[1]) if len(args) > 1 else None))
    elif cmd == "esperar-alcance":
        esperar_alcance(int(args[0]), float(args[1]), int(args[2]) if len(args) > 2 else 500)
    elif cmd == "esperar-estados":
        esperar_estados(int(args[0]), float(args[1]), args[2].split(","))
    elif cmd == "quiescer":
        quiescer(float(args[0]))
    elif cmd == "comparar":
        sys.exit(0 if comparar(args[0] if args else None) else 1)
    elif cmd == "oraculo":
        prefixo = None
        if "--prefixo" in args:
            i = args.index("--prefixo")
            prefixo, args = args[i + 1], args[:i] + args[i + 2:]
        sys.exit(0 if oraculo(args, prefixo) else 1)
    elif cmd == "ler":
        st, corpo = get_item(int(args[0]), args[1])
        print(f"HTTP {st} {corpo}")
    elif cmd == "contar":
        print(contar(int(args[0])))
    elif cmd == "apagar-tabela":
        apagar_tabela(int(args[0]))
    elif cmd == "reconstruir":
        sys.exit(0 if reconstruir(int(args[0])) else 1)
    elif cmd == "anotar":
        anotar(" ".join(args))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main(sys.argv[1:] or ["--help"])

"""Auxiliar das tasks de recuperação e reconstrução da POC local (O-43). Só biblioteca padrão.

Subcomandos:
  leader                          imprime N da célula líder (sai 1 sem líder)
  field N EXPR                    avalia EXPR com d = /status da célula N
  burst CELL COUNT [SIZE] [THREADS]
                                  grava COUNT itens pela API da célula (SIZE bytes de carga)
  until-out-of-window CELL [SIZE] grava pela líder até wal.janela[CELL] ficar falso
  wait-recovery CELL TIMEOUT STATES
                                  espera a célula passar pelos estados (ex.: recuperando ou
                                  reconstruindo,recuperando) e chegar a normal com o aplicado
                                  da líder; os estados curtos também valem pelo log
  compare                         confere os mesmos itens e _dr_v nas 3 tabelas (direto no LocalStack)
  trickle INTERVAL STOP [SIZE] [THREADS]
                                  grava pela líder a cada INTERVAL segundos até o arquivo STOP existir
"""
import concurrent.futures as cf
import json
import os
import subprocess
import sys
import time
import urllib.request

CELLS = (1, 2, 3)

# Credenciais fixas de teste (T-03), como no taskfile; o script também roda fora dele.
for k, v in (("AWS_ACCESS_KEY_ID", "test"), ("AWS_SECRET_ACCESS_KEY", "test"), ("AWS_DEFAULT_REGION", "sa-east-1"),
             ("AWS_PAGER", "")):
    os.environ.setdefault(k, v)


def status(n, timeout=2):
    with urllib.request.urlopen(f"http://localhost:909{n}/status", timeout=timeout) as r:
        return json.load(r)


def try_status(n):
    try:
        return status(n)
    except Exception:
        return None


def leader():
    for n in CELLS:
        d = try_status(n)
        if d and d.get("papel") == "lider":
            return n
    return None


def wait_leader(timeout=60):
    end = time.time() + timeout
    while time.time() < end:
        n = leader()
        if n:
            return n
        time.sleep(0.5)
    sys.exit("FAIL: sem líder")


def put(cell, pk, size):
    item = {"pk": {"S": pk}, "nome": {"S": "Maria"}}
    if size > 0:
        item["carga"] = {"S": "x" * size}
    body = json.dumps({"TableName": f"cadastro_cliente_cell_{cell}", "Item": item}).encode()
    req = urllib.request.Request(f"http://localhost:800{cell}", data=body, headers={
        "Content-Type": "application/x-amz-json-1.0", "X-Amz-Target": "DynamoDB_20120810.PutItem"})
    for _ in range(5):  # ThrottlingException e PENDING: repete, como o SDK
        try:
            with urllib.request.urlopen(req, timeout=15) as r:
                if r.status == 200:
                    return True
        except Exception:
            time.sleep(0.2)
    return False


def burst(cell, count, size=0, threads=8):
    base = time.time_ns()
    with cf.ThreadPoolExecutor(threads) as ex:
        ok = sum(ex.map(lambda i: put(cell, f"cli-rec-{base}-{i}", size), range(count)))
    print(f"burst: {ok}/{count} gravados pela cell-{cell}")
    return ok == count


def until_out_of_window(cell, size=1024):
    lead = wait_leader()
    base, i = time.time_ns(), 0
    end = time.time() + 600
    while time.time() < end:
        with cf.ThreadPoolExecutor(8) as ex:
            list(ex.map(lambda k: put(lead, f"cli-jan-{base}-{k}", size), range(i, i + 200)))
        i += 200
        d = try_status(lead) or {}
        jan = (d.get("wal") or {}).get("janela") or {}
        print(f"{i} itens; janela={jan} retido={(d.get('wal') or {}).get('retido')}")
        if jan.get(str(cell)) is False:
            return True
    return False


def trickle(interval, stop, size=0, threads=1):
    """Grava pela líder até o arquivo `stop` existir. O shell embutido do task não dá o PID
    de um processo em segundo plano ($!), então a parada é por arquivo, não por kill."""
    base, n, ok = time.time_ns(), [0], [0]

    def one():
        lead = leader()
        i = n[0]
        n[0] += 1
        if lead and put(lead, f"cli-lento-{base}-{i}", size):
            ok[0] += 1

    with cf.ThreadPoolExecutor(threads) as ex:
        while not os.path.exists(stop):
            list(ex.map(lambda _: one(), range(threads)))
            time.sleep(interval)
    print(f"trickle: {ok[0]}/{n[0]} gravados")


def logs_contain(cell, text, since):
    out = subprocess.run(["docker", "compose", "logs", "--no-log-prefix", "--since", since, f"cell-{cell}"],
                         capture_output=True, text=True).stdout
    return text in out


def wait_recovery(cell, timeout, states, since):
    want = [s for s in states.split(",") if s]
    seen, last = [], None
    end = time.time() + timeout
    while time.time() < end:
        d = try_status(cell)
        if d:
            st = d.get("modo", {}).get("estado")
            if st != last:
                print(f"cell-{cell}: {st} (aplicado {d.get('aplicado')})")
                last = st
                if st not in seen:
                    seen.append(st)
            lead = leader()
            ld = try_status(lead) if lead else None
            if st == "normal" and ld and d["aplicado"] >= ld["aplicado"]:
                evidence = {"recuperando": "recuperação concluída", "reconstruindo": "reconstrução concluída"}
                missing = [s for s in want if s not in seen and not logs_contain(cell, evidence[s], since)]
                if missing:
                    sys.exit(f"FAIL: cell-{cell} voltou a normal sem passar por {missing}")
                print(f"cell-{cell}: normal com o aplicado da líder ({d['aplicado']}); estados vistos {seen}")
                return True
        time.sleep(0.3)
    sys.exit(f"FAIL: cell-{cell} não voltou a normal em {timeout}s (vistos {seen})")


def scan_direct(n):
    out = subprocess.run(["aws", "dynamodb", "scan", "--endpoint-url", f"http://localhost:{4565 + n}",
                          "--table-name", f"cadastro_cliente_cell_{n}", "--output", "json"],
                         capture_output=True, text=True, check=True).stdout
    return {it["pk"]["S"]: it.get("_dr_v", {}).get("N") for it in json.loads(out)["Items"]}


def compare():
    tables = {n: scan_direct(n) for n in CELLS}
    ref = tables[1]
    ok = True
    for n in CELLS[1:]:
        if tables[n] != ref:
            only_a = sorted(set(ref) - set(tables[n]))[:5]
            only_b = sorted(set(tables[n]) - set(ref))[:5]
            diff_v = sorted(k for k in set(ref) & set(tables[n]) if ref[k] != tables[n][k])[:5]
            print(f"FAIL: cell-1 x cell-{n}: só na 1 {only_a}; só na {n} {only_b}; _dr_v diferente {diff_v}")
            ok = False
    if ok:
        print(f"as 3 tabelas têm os mesmos {len(ref)} itens e o mesmo _dr_v")
    return ok


def main(argv):
    cmd = argv[1]
    if cmd == "leader":
        n = leader()
        if not n:
            sys.exit(1)
        print(n)
    elif cmd == "field":
        d = status(int(argv[2]))
        print(eval(argv[3], {"d": d}))
    elif cmd == "burst":
        sys.exit(0 if burst(int(argv[2]), int(argv[3]), *(int(a) for a in argv[4:6])) else 1)
    elif cmd == "until-out-of-window":
        sys.exit(0 if until_out_of_window(int(argv[2]), *(int(a) for a in argv[3:4])) else 1)
    elif cmd == "wait-recovery":
        wait_recovery(int(argv[2]), int(argv[3]), argv[4], argv[5] if len(argv) > 5 else "10m")
    elif cmd == "trickle":
        trickle(float(argv[2]), argv[3], *(int(a) for a in argv[4:6]))
    elif cmd == "compare":
        sys.exit(0 if compare() else 1)
    else:
        sys.exit(f"subcomando desconhecido: {cmd}")


if __name__ == "__main__":
    main(sys.argv)

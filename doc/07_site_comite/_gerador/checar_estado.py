#!/usr/bin/env python3
"""Confere que o ESTADO_ATUAL.md, o inventário e o site acompanham os ADRs.

Falha (código 1) quando: um ADR não é citado no ESTADO_ATUAL.md; o "cobre ADR-001 a
ADR-0NN" do cabeçalho não é o último ADR; um ADR não tem linha no inventário; ou um ADR
não tem a decisão resumida em adr_resumo.py. Pega o esquecimento, não o conteúdo errado.
Uso: python3 checar_estado.py   (task check-estado, em DataReplicationApp)
"""
import glob, os, re, sys

AQUI = os.path.dirname(os.path.abspath(__file__))
PKG = os.path.dirname(os.path.dirname(AQUI))
sys.dont_write_bytecode = True  # não deixa __pycache__ no pacote
sys.path.insert(0, AQUI)
from adr_resumo import RESUMO  # noqa: E402


def ler(p):
    with open(p, encoding="utf-8") as f:
        return f.read()


def main():
    adrs = sorted(re.match(r"ADR-(\d{3})", os.path.basename(p)).group(1)
                  for p in glob.glob(os.path.join(PKG, "03_adr", "ADR-*.md")))
    estado = ler(os.path.join(PKG, "ESTADO_ATUAL.md"))
    inventario = ler(os.path.join(PKG, "03_adr", "INVENTARIO.md"))
    erros = []

    citados = set(re.findall(r"\]\(03_adr/ADR-(\d{3})-", estado))
    for n in adrs:
        if n not in citados:
            erros.append(f"ESTADO_ATUAL.md não cita o ADR-{n}")
    m = re.search(r"cobre ADR-001 a ADR-(\d{3})", estado)
    if not m:
        erros.append('ESTADO_ATUAL.md sem "cobre ADR-001 a ADR-0NN" no cabeçalho')
    elif m.group(1) != adrs[-1]:
        erros.append(f"ESTADO_ATUAL.md cobre até ADR-{m.group(1)}, mas o último é ADR-{adrs[-1]}")

    linhas = set(re.findall(r"^\| \[(\d{3})\]\(ADR-", inventario, flags=re.M))
    for n in adrs:
        if n not in linhas:
            erros.append(f"03_adr/INVENTARIO.md sem linha para o ADR-{n}")
    for n in adrs:
        if n not in RESUMO:
            erros.append(f"07_site_comite/_gerador/adr_resumo.py sem a decisão resumida do ADR-{n}")

    if erros:
        print("check-estado: FALHOU")
        for e in erros:
            print(f"  - {e}")
        sys.exit(1)
    print(f"check-estado: ok ({len(adrs)} ADRs, até o ADR-{adrs[-1]})")


if __name__ == "__main__":
    main()

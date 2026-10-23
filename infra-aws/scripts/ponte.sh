#!/usr/bin/env bash
# Port-forward em laço das 3 células (05_observabilidade_local.md, seção 0).
#   localhost:1909N -> cell-N-0:9090 (métricas, /status)
#   localhost:1800N -> cell-N-0:8000 (API, para escrita manual)
# Quando um pod reinicia o túnel cai; o laço reconecta 1 s depois de o pod voltar.
# Cada queda e reconexão vai para ponte.log com horário UTC: é o que separa "célula
# caiu" de "túnel caiu" na análise. Ctrl-C encerra os três.
set -u
LOG="${LOG:-$(dirname "$0")/../ponte.log}"

tunel() {
  local n="$1"
  while true; do
    echo "$(date -u +%FT%TZ) cell-$n conectando" >>"$LOG"
    kubectl --context "cluster-$n" -n dr port-forward "pod/cell-$n-0" \
      "1909$n:9090" "1800$n:8000" >/dev/null 2>&1
    echo "$(date -u +%FT%TZ) cell-$n caiu (saida $?)" >>"$LOG"
    sleep 1
  done
}

trap 'kill 0' INT TERM
for n in 1 2 3; do tunel "$n" & done
echo "túneis abertos; log em $LOG"
wait

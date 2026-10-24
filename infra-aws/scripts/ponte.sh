#!/usr/bin/env bash
# Port-forwards em laço para a sessão na AWS.
#   localhost:19090 -> svc/prometheus-server (cluster 1): é o que o Grafana local consulta
#   localhost:1909N -> cell-N-0:9090 (/status e /metrics, para olhar à mão)
#   localhost:1800N -> cell-N-0:8000 (API, para escrita manual)
# O Prometheus raspa de dentro da VPC: túnel caído aqui é buraco só no que você vê, não
# nos dados. Cada queda e reconexão vai para ponte.log com horário UTC. Ctrl-C encerra.
set -u
LOG="${LOG:-$(dirname "$0")/../ponte.log}"

laco() {
  local nome="$1"; shift
  while true; do
    echo "$(date -u +%FT%TZ) $nome conectando" >>"$LOG"
    kubectl "$@" >/dev/null 2>&1
    echo "$(date -u +%FT%TZ) $nome caiu (saida $?)" >>"$LOG"
    sleep 1
  done
}

trap 'kill 0' INT TERM
laco prometheus --context cluster-1 -n monitoramento port-forward svc/prometheus-server 19090:80 &
for n in 1 2 3; do
  laco "cell-$n" --context "cluster-$n" -n dr port-forward "pod/cell-$n-0" "1909$n:9090" "1800$n:8000" &
done
echo "túneis abertos (Prometheus em localhost:19090); log em $LOG"
wait

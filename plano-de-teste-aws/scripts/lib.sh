# Funções comuns dos cenários (source, não executar). Pressupõe o task conectar aberto em
# outro terminal: os túneis do infra-aws/scripts/ponte.sh (1909N e 1800N).
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RAIZ="$(cd "$DIR/../.." && pwd)"
INFRA="$RAIZ/infra-aws"
RES="$INFRA/resultados" # fora do git (infra-aws/.gitignore); o task k6:coletar grava aqui
T="python3 $DIR/drtool_aws.py"

log() { echo "$(date +%H:%M:%S) $*"; }
falha() { echo "FAIL: $*" >&2; exit 1; }

# Nome único por execução: o k6 usa cli#<CENARIO>- como prefixo da pk e o oráculo filtra por ele.
nome_execucao() { echo "$1-$(date +%m%d-%H%M%S)"; }

# ---------- k6 (pod k6 do cluster 1) ----------
# Roda desacoplado dentro do pod (nohup): um túnel do kubectl que cai não mata a carga.
K6_ATIVO=""
k6_iniciar() { # CENARIO TPS ENTRADA DURACAO
  local c="$1" tps="$2" entrada="$3" duracao="$4"
  kubectl --context cluster-1 -n teste exec k6 -- sh -c "
    if pgrep -x k6 >/dev/null; then echo 'já existe um k6 rodando no pod'; exit 1; fi
    nohup k6 run -e CENARIO=$c -e PERFIL=constante -e TPS=$tps -e DURACAO=$duracao -e ENTRADA=$entrada \
      --out json=/out/$c.json --summary-export=/out/$c-resumo.json /scripts/carga.js \
      </dev/null >/out/$c.log 2>&1 &"
  sleep 5
  kubectl --context cluster-1 -n teste exec k6 -- pgrep -x k6 >/dev/null \
    || { kubectl --context cluster-1 -n teste exec k6 -- tail -n 20 "/out/$c.log"; falha "o k6 não subiu"; }
  K6_ATIVO="$c"
  log "k6: $tps TPS, entrada=$entrada, cenário $c (log em /out/$c.log no pod)"
  kubectl --context cluster-1 -n teste exec k6 -- grep -m1 'cenario=' "/out/$c.log" || true
}

k6_parar() {
  [ -n "$K6_ATIVO" ] || return 0
  log "k6: encerrando (SIGINT; o resumo é gravado)"
  kubectl --context cluster-1 -n teste exec k6 -- sh -c \
    'pkill -INT -x k6 || true; for i in $(seq 60); do pgrep -x k6 >/dev/null || exit 0; sleep 1; done; pkill -KILL -x k6'
  kubectl --context cluster-1 -n teste exec k6 -- sh -c "grep -E 'http_req_failed|http_reqs|dr_ledger' /out/$K6_ATIVO.log | tail -n 5" || true
  K6_ATIVO=""
}

# ---------- células ----------
# Uma célula por StatefulSet (cell-N, 1 réplica). Apagar o pod não basta: o StatefulSet o
# recria na hora. Escalar para 0 mantém o PVC (mesmo WAL, mesma identidade).
CELULA_PARADA=""
parar_celula() { # N MODO(graceful|abrupto)
  local n="$1" modo="$2"
  CELULA_PARADA="$n"
  kubectl --context "cluster-$n" -n dr scale statefulset "cell-$n" --replicas=0 >/dev/null
  if [ "$modo" = "abrupto" ]; then
    # Sem os 25 s do fx.StopTimeout: o processo morre sem fechar nada (≈ SIGKILL).
    kubectl --context "cluster-$n" -n dr delete pod "cell-$n-0" --grace-period=0 --force --ignore-not-found >/dev/null 2>&1 || true
  fi
  kubectl --context "cluster-$n" -n dr wait --for=delete "pod/cell-$n-0" --timeout=120s >/dev/null 2>&1 || true
  log "cell-$n fora ($modo)"
}

voltar_celula() { # N
  local n="$1"
  kubectl --context "cluster-$n" -n dr scale statefulset "cell-$n" --replicas=1 >/dev/null
  CELULA_PARADA=""
  for _ in $(seq 120); do
    kubectl --context "cluster-$n" -n dr get pod "cell-$n-0" >/dev/null 2>&1 && break
    sleep 1
  done
  log "cell-$n de volta (pod criado; a prontidão só vem com a célula em normal)"
}

# Garante, mesmo com Ctrl-C ou erro, que a célula volta e o k6 para.
restaurar() {
  local rc=$?
  [ -z "$CELULA_PARADA" ] || { log "restaurando cell-$CELULA_PARADA"; voltar_celula "$CELULA_PARADA" || true; }
  k6_parar || true
  exit $rc
}
trap restaurar EXIT

# ---------- fim de cenário ----------
fechar_cenario() { # CENARIO [LEDGER_EXTRA...]
  local c="$1"; shift
  $T quiescer 600
  (cd "$INFRA" && task k6:coletar CENARIO="$c")
  $T status
  log "oráculo do RPO (ORC-04) sobre o ledger do k6"
  $T oraculo "$RES/$c/ledger.json.gz" "$@"
}

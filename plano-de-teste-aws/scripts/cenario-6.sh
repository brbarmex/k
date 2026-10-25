#!/usr/bin/env bash
# Cenário 6: corte de energia no nó de uma célula sob carga (reboot imediato pelo
# sysrq, sem sync nem desmontar o disco). Prova a durabilidade: nenhuma escrita
# confirmada se perde (oráculo R1) e o WAL não volta atrás do que a célula confirmou (FD-01 A6).
source "$(dirname "$0")/lib.sh"
ALVO="${ALVO:-lider}"             # lider | follower
TPS="${TPS:-300}"
AQUECIMENTO="${AQUECIMENTO:-60}"
TOL="${TOL:-$TPS}"
TIMEOUT="${TIMEOUT:-900}"
C=$(nome_execucao c6)

$T ping
LIDER=$($T leader) || falha "sem líder"
if [ "$ALVO" = "lider" ]; then
  X="$LIDER"; ENTRADA=follower
else
  X="${CELL:-$($T follower)}"; ENTRADA="$LIDER"
  [ "$X" != "$LIDER" ] || falha "cell-$X é a líder"
fi
CTX="cluster-$X"
NO=$(kubectl --context "$CTX" -n dr get pod "cell-$X-0" -o jsonpath='{.spec.nodeName}')
log "cenário 6 ($C): corte de energia no nó $NO da cell-$X ($ALVO), $TPS TPS com entrada=$ENTRADA"

limpar_falha() { kubectl --context "$CTX" delete pod falha --ignore-not-found --wait=false >/dev/null 2>&1 || true; }
trap 'rc=$?; limpar_falha; (exit $rc); restaurar' EXIT

# Pod privilegiado no nó da célula (infra-aws/falhas/netshoot.yaml), preparado antes da carga.
kubectl --context "$CTX" apply -f "$INFRA/falhas/netshoot.yaml" >/dev/null
kubectl --context "$CTX" wait --for=condition=Ready pod/falha --timeout=3m >/dev/null

k6_iniciar "$C" "$TPS" "$ENTRADA" 30m
sleep "$AQUECIMENTO"

# FD-01 A6, só com ALVO=follower: o match é o que a líder sabe que a cell-X já gravou no
# WAL com fsync. Na líder não há esse número: ela manda o MsgApp antes do próprio fsync (ADR-056).
MATCH_ANTES=""
if [ "$ALVO" = "follower" ]; then
  MATCH_ANTES=$($T campo "$LIDER" "d.get('match', {}).get('$X', 0)")
  log "antes do corte: a líder cell-$LIDER vê a cell-$X com match=$MATCH_ANTES"
fi

$T anotar "$C: T0 corte de energia no nó da cell-$X"
# b = reboot imediato do kernel, sem sync: o que não passou pelo fsync se perde, como numa queda de luz.
# O exec nunca volta (o nó some): sai em segundo plano e é encerrado depois de 15 s.
kubectl --context "$CTX" exec falha -- nsenter -t 1 -m -u -i -n -p -- \
  sh -c 'echo 1 > /proc/sys/kernel/sysrq; echo b > /proc/sysrq-trigger' >/dev/null 2>&1 &
CORTE=$!
sleep 15; kill "$CORTE" 2>/dev/null || true
log "sysrq-b enviado ao nó $NO"

if [ "$ALVO" = "lider" ]; then
  NOVA=$($T esperar-lider 60 "$LIDER")
  $T anotar "$C: nova líder cell-$NOVA"
fi

log "esperando o nó $NO voltar (NotReady -> Ready)"
for _ in $(seq 120); do
  [ "$(kubectl --context "$CTX" get node "$NO" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" != "True" ] && break
  sleep 2
done
kubectl --context "$CTX" wait --for=condition=Ready "node/$NO" --timeout=15m >/dev/null
$T anotar "$C: nó da cell-$X de volta"
limpar_falha

VOLTA=$(date +%s)
$T esperar-alcance "$X" "$TIMEOUT" "$TOL"
$T anotar "$C: cell-$X alcançou ($(( $(date +%s) - VOLTA )) s depois do nó voltar)"

RELIDO=$(kubectl --context "$CTX" -n dr logs "cell-$X-0" 2>/dev/null | grep -m1 'partida: WAL relido' || true)
log "partida da cell-$X: ${RELIDO:-(linha 'partida: WAL relido' não encontrada no log)}"
A6=0
if [ -n "$MATCH_ANTES" ] && [ -n "$RELIDO" ]; then
  # O maior índice relido do disco: a última entrada da cauda, ou o snapshot se a cauda veio vazia.
  ULTIMO=$(printf '%s' "$RELIDO" | python3 -c 'import json,sys; d=json.loads(sys.stdin.read()); print(max(d.get("ultimo", 0), d.get("snapshot", 0)))')
  if [ "$ULTIMO" -ge "$MATCH_ANTES" ]; then
    log "FD-01 A6 OK: o WAL relido vai até $ULTIMO >= match $MATCH_ANTES visto pela líder antes do corte"
  else
    log "FD-01 A6 FALHOU: o WAL relido vai até $ULTIMO < match $MATCH_ANTES: a célula confirmou o que não guardou"
    A6=1
  fi
fi

k6_parar
fechar_cenario "$C"
[ "$A6" = 0 ] || falha "cenário 6: regressão do WAL (FD-01 A6)"
log "PASS: cenário 6"

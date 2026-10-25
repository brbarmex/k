#!/usr/bin/env bash
# Cenário 3: TPS na líder; depois de AQUECIMENTO s uma follower para por FORA s; o
# cenário termina quando ela alcança a líder (estado normal e atraso <= TOL entradas).
source "$(dirname "$0")/lib.sh"
TPS="${TPS:-500}"
AQUECIMENTO="${AQUECIMENTO:-60}"
FORA="${FORA:-180}"
MODO="${MODO:-graceful}"          # graceful (SIGTERM, como um stop) | abrupto (≈ SIGKILL)
TOL="${TOL:-$TPS}"                # ~1 s de entradas: sob carga o commit da líder não para
TIMEOUT="${TIMEOUT:-900}"
C=$(nome_execucao c3)

$T ping
LIDER=$($T leader) || falha "sem líder"
FOLLOWER="${CELL:-$($T follower)}"
[ "$FOLLOWER" != "$LIDER" ] || falha "cell-$FOLLOWER é a líder; escolha uma follower"
log "cenário 3 ($C): $TPS TPS na líder cell-$LIDER; cell-$FOLLOWER fora por ${FORA}s depois de ${AQUECIMENTO}s"
$T status

# ENTRADA=<N da líder>: o carga.js só descobre a líder no setup, e ela não muda neste cenário.
k6_iniciar "$C" "$TPS" "$LIDER" 30m
sleep "$AQUECIMENTO"

$T anotar "$C: T0 cell-$FOLLOWER para ($MODO)"
parar_celula "$FOLLOWER" "$MODO"
INICIO_FORA=$(date +%s)
sleep 5; $T status
RESTO=$((FORA - ($(date +%s) - INICIO_FORA))); [ "$RESTO" -le 0 ] || sleep "$RESTO"

$T anotar "$C: cell-$FOLLOWER volta"
voltar_celula "$FOLLOWER"
VOLTA=$(date +%s)
$T esperar-alcance "$FOLLOWER" "$TIMEOUT" "$TOL"
$T anotar "$C: cell-$FOLLOWER alcançou a líder ($(( $(date +%s) - VOLTA )) s depois de voltar)"

k6_parar
fechar_cenario "$C"
log "PASS: cenário 3 (MTTR da cell-$FOLLOWER: $(( $(date +%s) - VOLTA )) s até o fim do oráculo; ver o tempo de alcance acima)"

#!/usr/bin/env bash
# Cenário 4: TPS pelas followers; a líder cai, outra assume; a antiga fica FORA s e, ao
# voltar (como follower), alcança as demais.
source "$(dirname "$0")/lib.sh"
TPS="${TPS:-300}"
AQUECIMENTO="${AQUECIMENTO:-60}"
FORA="${FORA:-60}"
MODO="${MODO:-abrupto}"           # abrupto (≈ SIGKILL) | graceful (SIGTERM)
TOL="${TOL:-$TPS}"
TIMEOUT="${TIMEOUT:-900}"
POS="${POS:-30}"                  # carga depois do alcance, para ver a latência estabilizar (RTO-06)
C=$(nome_execucao c4)

$T ping
LIDER=$($T leader) || falha "sem líder"
log "cenário 4 ($C): $TPS TPS pelas followers; a líder cell-$LIDER cai depois de ${AQUECIMENTO}s e fica ${FORA}s fora"
$T status

# ENTRADA=follower: as duas que sobrevivem. Pela líder, toda a carga falharia durante a
# queda (o carga.js escolhe os alvos no setup e não redireciona).
k6_iniciar "$C" "$TPS" follower 30m
sleep "$AQUECIMENTO"

$T anotar "$C: T0 líder cell-$LIDER cai ($MODO)"
T0=$(python3 -c "import time; print(time.time())")
parar_celula "$LIDER" "$MODO"
NOVA=$($T esperar-lider 60 "$LIDER")
log "nova líder: cell-$NOVA (T_eleição visto pelo /status: $(python3 -c "import time; print(f'{time.time()-$T0:.1f}')") s desde o T0)"
$T anotar "$C: nova líder cell-$NOVA"
$T status
sleep "$(python3 -c "import time; print(max(0, $FORA - (time.time() - $T0)))")"

$T anotar "$C: cell-$LIDER volta"
voltar_celula "$LIDER"
VOLTA=$(date +%s)
$T esperar-alcance "$LIDER" "$TIMEOUT" "$TOL"
$T anotar "$C: cell-$LIDER alcançou ($(( $(date +%s) - VOLTA )) s depois de voltar)"
PAPEL=$($T campo "$LIDER" 'd["papel"]')
log "cell-$LIDER voltou como $PAPEL (o Raft não devolve a liderança; a líder segue cell-$($T leader))"
sleep "$POS"

k6_parar
fechar_cenario "$C"
log "PASS: cenário 4"

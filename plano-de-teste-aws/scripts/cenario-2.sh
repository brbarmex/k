#!/usr/bin/env bash
# Cenário 2: QTD escritas por uma follower são encaminhadas à líder e chegam às 3 tabelas.
source "$(dirname "$0")/lib.sh"
QTD="${QTD:-3}"
C=$(nome_execucao c2)

$T ping
LIDER=$($T leader) || falha "sem líder"
FOLLOWER="${CELL:-$($T follower)}"
[ "$FOLLOWER" != "$LIDER" ] || falha "cell-$FOLLOWER é a líder; escolha uma follower"

ANTES=$($T metrica "$FOLLOWER" dr_forwards_total)
log "cenário 2 ($C): $QTD escritas pela follower cell-$FOLLOWER (líder cell-$LIDER)"
$T anotar "$C: $QTD escritas pela follower cell-$FOLLOWER"
# A escrita usa a tabela da própria follower (cadastro_cliente_cell_N): outra tabela dá 400 (CA-10).
$T escrever "$FOLLOWER" "$QTD" "cli#$C-" "$RES/$C/escritas.jsonl" || falha "nem todas as escritas foram confirmadas"
DEPOIS=$($T metrica "$FOLLOWER" dr_forwards_total)
ENCAMINHADAS=$((DEPOIS - ANTES))
log "dr_forwards_total na cell-$FOLLOWER: $ANTES -> $DEPOIS (+$ENCAMINHADAS)"
[ "$ENCAMINHADAS" -ge "$QTD" ] || falha "esperava +$QTD encaminhamentos à líder, vi +$ENCAMINHADAS"

$T quiescer 120
$T oraculo "$RES/$C/escritas.jsonl"
log "PASS: cenário 2 (as $QTD foram encaminhadas à líder cell-$LIDER e estão nas 3 tabelas)"

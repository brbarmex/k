#!/usr/bin/env bash
# Cenário 1: QTD escritas pela líder chegam às 3 tabelas com o mesmo _dr_v.
source "$(dirname "$0")/lib.sh"
QTD="${QTD:-10}"
C=$(nome_execucao c1)

$T ping
LIDER=$($T leader) || falha "sem líder"
log "cenário 1 ($C): $QTD escritas pela líder cell-$LIDER"
$T anotar "$C: $QTD escritas pela líder cell-$LIDER"
$T escrever "$LIDER" "$QTD" "cli#$C-" "$RES/$C/escritas.jsonl" || falha "nem todas as escritas foram confirmadas"
# O ACK da líder não espera o apply das followers (AP-09): espera a quiescência antes de ler.
$T quiescer 120
$T oraculo "$RES/$C/escritas.jsonl"
log "PASS: cenário 1"

#!/usr/bin/env bash
# Cenário 5: apaga TODOS os itens da tabela de UMA follower direto no DynamoDB (sem passar
# pela camada) e recupera pela reconstrução (POST /admin/reconstruir: CopyTable de uma
# célula em normal, DC-11), com carga leve pela líder durante a cópia.
#
# A camada não percebe sozinha um apagamento direto (FB-04): sem a reconstrução manual, a
# follower ficaria divergente para sempre e a leitura forte por ela devolveria "não existe".
source "$(dirname "$0")/lib.sh"
SEMENTE="${SEMENTE:-200}"         # itens gravados antes do apagamento, para provar a cópia
TPS="${TPS:-50}"                  # carga durante a reconstrução (as escritas na cópia têm de chegar)
TIMEOUT="${TIMEOUT:-1800}"
C=$(nome_execucao c5)

$T ping
LIDER=$($T leader) || falha "sem líder"
ALVO="${CELL:-$($T follower)}"
# A líder não se reconstrói (409). Para a variante na líder, veja o roteiro (cenário 5b).
[ "$ALVO" != "$LIDER" ] || falha "cell-$ALVO é a líder; a reconstrução só vale numa follower"
log "cenário 5 ($C): apagar a tabela da follower cell-$ALVO e reconstruir a partir das outras"

log "1. semente: $SEMENTE escritas pela líder cell-$LIDER"
$T escrever "$LIDER" "$SEMENTE" "cli#$C-semente-" "$RES/$C/escritas.jsonl" >/dev/null || falha "semente incompleta"
$T quiescer 120
log "2. antes: as 3 tabelas iguais (tabela inteira)"
$T comparar || falha "as tabelas já estavam divergentes antes do cenário: investigue antes de seguir"

PK=$(python3 -c "import json,sys; print(json.loads(open(sys.argv[1]).readline())['pk'])" "$RES/$C/escritas.jsonl")
$T anotar "$C: T0 apagando a tabela cadastro_cliente_cell_$ALVO direto no DynamoDB"
log "3. apagando todos os itens de cadastro_cliente_cell_$ALVO"
$T apagar-tabela "$ALVO"

log "4. o que o cliente vê agora (a camada não detecta o apagamento)"
echo -n "  leitura forte pela cell-$ALVO:  "; $T ler "$ALVO" "$PK"
echo -n "  leitura forte pela cell-$LIDER: "; $T ler "$LIDER" "$PK"
$T comparar && falha "o comparador não acusou a divergência" || log "divergência detectada pelo comparador (esperado)"

log "5. reconstrução sob carga de $TPS TPS pela líder"
k6_iniciar "$C" "$TPS" "$LIDER" 60m
sleep 10
$T anotar "$C: POST /admin/reconstruir na cell-$ALVO"
$T reconstruir "$ALVO" || falha "a reconstrução não foi aceita"
# reconstruindo (CopyTable) -> reinício (DC-04) -> recuperando (CatchUp) -> normal
$T esperar-estados "$ALVO" "$TIMEOUT" reconstruindo,recuperando
$T anotar "$C: cell-$ALVO reconstruída"
$T esperar-alcance "$ALVO" 600 "$TPS"

k6_parar
log "6. depois: semente e carga nas 3 tabelas, e as 3 tabelas inteiras iguais"
fechar_cenario "$C" "$RES/$C/escritas.jsonl"
$T comparar || falha "as tabelas inteiras ainda divergem depois da reconstrução"
echo -n "  leitura forte pela cell-$ALVO: "; $T ler "$ALVO" "$PK"
log "PASS: cenário 5"

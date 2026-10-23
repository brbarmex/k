#!/usr/bin/env bash
# Confere, depois do destroy, que nada da POC ficou cobrando (02_terraform.md, seção 7).
# Sai com código 1 se achar algo.
set -u
REGIAO="${REGIAO:-sa-east-1}"
PROJETO="${PROJETO:-dr-poc}"
achou=0

secao() { printf '\n== %s\n' "$1"; }
mostra() {
  if [ -n "$1" ]; then echo "$1"; achou=1; else echo "nada"; fi
}

secao "Recursos com a etiqueta projeto=$PROJETO (fora da stack conta)"
mostra "$(aws resourcegroupstaggingapi get-resources --region "$REGIAO" \
  --tag-filters "Key=projeto,Values=$PROJETO" \
  --query "ResourceTagMappingList[?!(Tags[?Key=='stack' && Value=='conta'])].ResourceARN" --output text)"

secao "Clusters EKS"
mostra "$(aws eks list-clusters --region "$REGIAO" --query 'clusters[?starts_with(@, `'"$PROJETO"'`)]' --output text)"

secao "Volumes EBS soltos (available)"
mostra "$(aws ec2 describe-volumes --region "$REGIAO" --filters Name=status,Values=available \
  --query 'Volumes[].[VolumeId,Size,CreateTime]' --output text)"

secao "IPs elásticos"
mostra "$(aws ec2 describe-addresses --region "$REGIAO" --query 'Addresses[].[PublicIp,AllocationId]' --output text)"

secao "Load balancers"
mostra "$(aws elbv2 describe-load-balancers --region "$REGIAO" --query 'LoadBalancers[].LoadBalancerArn' --output text)"

secao "NAT Gateways"
mostra "$(aws ec2 describe-nat-gateways --region "$REGIAO" --filter Name=state,Values=available,pending \
  --query 'NatGateways[].NatGatewayId' --output text)"

secao "Tabelas DynamoDB da POC"
mostra "$(aws dynamodb list-tables --region "$REGIAO" --query 'TableNames[?starts_with(@, `cadastro_cliente_cell_`)]' --output text)"

secao "Log groups do EKS"
mostra "$(aws logs describe-log-groups --region "$REGIAO" --log-group-name-prefix /aws/eks \
  --query 'logGroups[].logGroupName' --output text)"

secao "Zona privada dr.internal"
mostra "$(aws route53 list-hosted-zones-by-name --dns-name dr.internal \
  --query 'HostedZones[?Name==`dr.internal.`].Id' --output text)"

echo
if [ "$achou" = 1 ]; then
  echo "ATENÇÃO: sobrou algo acima. Apague e anote a causa."
  exit 1
fi
echo "Limpo."

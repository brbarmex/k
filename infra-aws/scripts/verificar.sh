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

# A busca por etiqueta segue listando recursos já apagados por horas depois do destroy:
# cada ARN é conferido no serviço dono. Tipo sem conferência conta como existente.
# Sucesso = existe; NotFound = já apagado; qualquer outro erro (permissão, throttling) = existe.
achado() { # comando aws...
  local saida
  saida=$("$@" --region "$REGIAO" --output text 2>&1) && return 0
  ! grep -q -E 'NotFound|does not exist' <<<"$saida"
}

existe() { # ARN
  local arn="$1" svc tipo resto id
  svc=$(cut -d: -f3 <<<"$arn"); resto=$(cut -d: -f6- <<<"$arn")
  tipo=${resto%%/*}; id=${resto#*/}
  case "$svc" in
    ec2) # o describe-tags também lista apagados: vai no describe de cada tipo
      case "$tipo" in
        volume)              achado aws ec2 describe-volumes --volume-ids "$id" ;;
        security-group)      achado aws ec2 describe-security-groups --group-ids "$id" ;;
        security-group-rule) achado aws ec2 describe-security-group-rules --security-group-rule-ids "$id" ;;
        vpc-endpoint)        achado aws ec2 describe-vpc-endpoints --vpc-endpoint-ids "$id" ;;
        vpc)                 achado aws ec2 describe-vpcs --vpc-ids "$id" ;;
        subnet)              achado aws ec2 describe-subnets --subnet-ids "$id" ;;
        network-interface)   achado aws ec2 describe-network-interfaces --network-interface-ids "$id" ;;
        instance) # terminada continua listada por um tempo
          [ "$(aws ec2 describe-instances --region "$REGIAO" --instance-ids "$id" \
            --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null)" != terminated ] ;;
        *) return 0 ;;
      esac ;;
    eks) # cluster/<c>, nodegroup/<c>/..., addon/<c>/..., podidentityassociation/<c>/...: some com o cluster
      aws eks describe-cluster --region "$REGIAO" --name "${id%%/*}" >/dev/null 2>&1 ;;
    dynamodb)
      aws dynamodb describe-table --region "$REGIAO" --table-name "$id" >/dev/null 2>&1 ;;
    *) return 0 ;;
  esac
}

secao "Recursos com a etiqueta projeto=$PROJETO (fora da stack conta)"
restantes=""
for arn in $(aws resourcegroupstaggingapi get-resources --region "$REGIAO" \
  --tag-filters "Key=projeto,Values=$PROJETO" \
  --query "ResourceTagMappingList[?!(Tags[?Key=='stack' && Value=='conta'])].ResourceARN" --output text); do
  if existe "$arn"; then restantes+="$arn"$'\n'; fi
done
mostra "${restantes%$'\n'}"

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

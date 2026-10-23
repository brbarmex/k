"""Autodestruição da POC na AWS (infra-aws/terraform/conta/autodestruicao.tf).

Roda a cada 15 min. Apaga o que tem a etiqueta projeto=<PROJETO> e a etiqueta
expira-em (RFC 3339, UTC) no passado:

1. node groups dos clusters EKS vencidos (para o custo de EC2 na hora);
2. o cluster, quando ele já não tem node group (numa próxima rodada);
3. tabelas DynamoDB vencidas;
4. volumes EBS soltos (available) com a etiqueta, só quando não sobra cluster da POC.

Com SIMULACAO=true só registra o que apagaria. É idempotente: cada rodada continua de
onde a anterior parou.
"""

import datetime
import json
import os

import boto3

PROJETO = os.environ["PROJETO"]
TOPICO = os.environ.get("TOPICO", "")
SIMULACAO = os.environ.get("SIMULACAO", "false").lower() == "true"

eks = boto3.client("eks")
dynamodb = boto3.client("dynamodb")
ec2 = boto3.client("ec2")
sns = boto3.client("sns")


def vencido(tags, agora):
    """True se as etiquetas são da POC e o expira-em já passou (ou falta, por segurança)."""
    if tags.get("projeto") != PROJETO:
        return False
    expira = tags.get("expira-em")
    if not expira:
        return True  # recurso da POC sem prazo: trata como vencido
    try:
        quando = datetime.datetime.fromisoformat(expira.replace("Z", "+00:00"))
    except ValueError:
        return True
    return quando <= agora


def clusters_da_poc():
    nomes = []
    for pagina in eks.get_paginator("list_clusters").paginate():
        nomes.extend(pagina["clusters"])
    da_poc = []
    for nome in nomes:
        cluster = eks.describe_cluster(name=nome)["cluster"]
        if cluster.get("tags", {}).get("projeto") == PROJETO:
            da_poc.append(cluster)
    return da_poc


def limpar_clusters(agora, feito):
    restantes = 0
    for cluster in clusters_da_poc():
        nome = cluster["name"]
        restantes += 1
        if not vencido(cluster.get("tags", {}), agora):
            continue
        if cluster["status"] == "DELETING":
            continue
        grupos = eks.list_nodegroups(clusterName=nome)["nodegroups"]
        if grupos:
            for grupo in grupos:
                feito.append(f"node group {nome}/{grupo}")
                if not SIMULACAO:
                    try:
                        eks.delete_nodegroup(clusterName=nome, nodegroupName=grupo)
                    except eks.exceptions.ResourceInUseException:
                        pass  # já está sendo apagado
        else:
            feito.append(f"cluster {nome}")
            if not SIMULACAO:
                eks.delete_cluster(name=nome)
    return restantes


def limpar_tabelas(agora, feito):
    nomes = []
    for pagina in dynamodb.get_paginator("list_tables").paginate():
        nomes.extend(pagina["TableNames"])
    for nome in nomes:
        arn = dynamodb.describe_table(TableName=nome)["Table"]["TableArn"]
        tags = {t["Key"]: t["Value"] for t in dynamodb.list_tags_of_resource(ResourceArn=arn).get("Tags", [])}
        if vencido(tags, agora):
            feito.append(f"tabela {nome}")
            if not SIMULACAO:
                try:
                    dynamodb.delete_table(TableName=nome)
                except dynamodb.exceptions.ResourceInUseException:
                    pass


def limpar_volumes(feito):
    filtros = [
        {"Name": "status", "Values": ["available"]},
        {"Name": "tag:projeto", "Values": [PROJETO]},
    ]
    for pagina in ec2.get_paginator("describe_volumes").paginate(Filters=filtros):
        for volume in pagina["Volumes"]:
            feito.append(f"volume {volume['VolumeId']} ({volume['Size']} GiB)")
            if not SIMULACAO:
                ec2.delete_volume(VolumeId=volume["VolumeId"])


def handler(event, context):
    agora = datetime.datetime.now(datetime.timezone.utc)
    feito = []
    restantes = limpar_clusters(agora, feito)
    limpar_tabelas(agora, feito)
    if restantes == 0:
        limpar_volumes(feito)

    resumo = {"agora": agora.isoformat(), "simulacao": SIMULACAO, "acoes": feito}
    print(json.dumps(resumo, ensure_ascii=False))
    if feito and TOPICO:
        sns.publish(
            TopicArn=TOPICO,
            Subject=f"[{PROJETO}] autodestruição: {len(feito)} ação(ões)",
            Message=json.dumps(resumo, ensure_ascii=False, indent=2),
        )
    return resumo

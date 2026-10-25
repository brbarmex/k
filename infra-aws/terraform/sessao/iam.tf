# Pod Identity: nenhum pod recebe chave estática. Atenção: AWS_ACCESS_KEY_ID e
# AWS_SECRET_ACCESS_KEY do docker-compose NÃO podem ir para o manifesto (ganham do
# Pod Identity e o DynamoDB real recusa).

data "aws_caller_identity" "atual" {}

# Só os clusters da POC nesta conta assumem as roles (confused deputy). O ARN é montado
# pelo nome, e não lido do módulo, porque o add-on do EBS CSI usa a role dentro do módulo.
data "aws_iam_policy_document" "pod_identity" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole", "sts:TagSession"]
    principals {
      type        = "Service"
      identifiers = ["pods.eks.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.atual.account_id]
    }
    condition {
      test     = "ArnLike"
      variable = "aws:SourceArn"
      values   = ["arn:aws:eks:${var.regiao}:${data.aws_caller_identity.atual.account_id}:cluster/${var.projeto}-cell-*"]
    }
  }
}

# --- App (dr/datareplication): as 3 tabelas, para não cair no applier parado por AccessDenied.
resource "aws_iam_role" "app" {
  name               = "${var.projeto}-app"
  assume_role_policy = data.aws_iam_policy_document.pod_identity.json
}

resource "aws_iam_role_policy" "app" {
  name = "dynamodb"
  role = aws_iam_role.app.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["dynamodb:PutItem", "dynamodb:GetItem", "dynamodb:Scan", "dynamodb:DescribeTable"]
      Resource = [for t in aws_dynamodb_table.cadastro : t.arn]
    }]
  })
}

resource "aws_eks_pod_identity_association" "app" {
  for_each = var.celulas

  cluster_name    = module.eks[each.key].cluster_name
  namespace       = "dr"
  service_account = "datareplication"
  role_arn        = aws_iam_role.app.arn
}

# --- EBS CSI (kube-system/ebs-csi-controller-sa), associado pelo add-on.
resource "aws_iam_role" "ebs_csi" {
  name               = "${var.projeto}-ebs-csi"
  assume_role_policy = data.aws_iam_policy_document.pod_identity.json
}

resource "aws_iam_role_policy_attachment" "ebs_csi" {
  role       = aws_iam_role.ebs_csi.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

# --- ExternalDNS (external-dns/external-dns): só a zona privada da POC.
resource "aws_iam_role" "external_dns" {
  name               = "${var.projeto}-external-dns"
  assume_role_policy = data.aws_iam_policy_document.pod_identity.json
}

resource "aws_iam_role_policy" "external_dns" {
  name = "route53"
  role = aws_iam_role.external_dns.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["route53:ChangeResourceRecordSets"]
        Resource = aws_route53_zone.privada.arn
      },
      {
        Effect   = "Allow"
        Action   = ["route53:ListHostedZones", "route53:ListResourceRecordSets", "route53:ListTagsForResources"]
        Resource = "*"
      },
    ]
  })
}

resource "aws_eks_pod_identity_association" "external_dns" {
  for_each = var.celulas

  cluster_name    = module.eks[each.key].cluster_name
  namespace       = "external-dns"
  service_account = "external-dns"
  role_arn        = aws_iam_role.external_dns.arn
}

# --- Prometheus (monitoramento/prometheus-server, só no cluster 1): ec2_sd_configs acha os
# nós dos 3 clusters pela etiqueta eks:cluster-name e raspa node-exporter e cAdvisor pelo IP.
resource "aws_iam_role" "prometheus" {
  name               = "${var.projeto}-prometheus"
  assume_role_policy = data.aws_iam_policy_document.pod_identity.json
}

resource "aws_iam_role_policy" "prometheus" {
  name = "ec2-sd"
  role = aws_iam_role.prometheus.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["ec2:DescribeInstances", "ec2:DescribeAvailabilityZones"]
      Resource = "*"
    }]
  })
}

resource "aws_eks_pod_identity_association" "prometheus" {
  cluster_name    = module.eks["1"].cluster_name
  namespace       = "monitoramento"
  service_account = "prometheus-server"
  role_arn        = aws_iam_role.prometheus.arn
}

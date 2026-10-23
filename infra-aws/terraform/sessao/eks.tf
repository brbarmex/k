# Três clusters EKS, um por célula (decisão 1b). Padrões do módulo que custam dinheiro
# ficam desligados: log do plano de controle, log group e chave KMS.

module "eks" {
  source   = "terraform-aws-modules/eks/aws"
  version  = "~> 21.0"
  for_each = var.celulas

  name               = "${var.projeto}-cell-${each.key}"
  kubernetes_version = var.kubernetes_version

  endpoint_public_access       = true
  endpoint_public_access_cidrs = [var.meu_ip_cidr]

  enabled_log_types           = []
  create_cloudwatch_log_group = false
  create_kms_key              = false
  encryption_config           = null

  enable_cluster_creator_admin_permissions = true

  vpc_id = module.vpc.vpc_id
  # O plano de controle exige subnets em pelo menos 2 AZs; os nós ficam só na AZ da célula.
  control_plane_subnet_ids = module.vpc.public_subnets
  subnet_ids               = [local.subnet_da_celula[each.key]]

  addons = {
    vpc-cni = {
      before_compute = true # antes dos nós: o max-pods já nasce com prefixos
      configuration_values = jsonencode({
        env = { ENABLE_PREFIX_DELEGATION = "true" }
      })
    }
    kube-proxy             = {}
    coredns                = {}
    eks-pod-identity-agent = { before_compute = true }
    aws-ebs-csi-driver = {
      pod_identity_association = [{
        role_arn        = aws_iam_role.ebs_csi.arn
        service_account = "ebs-csi-controller-sa"
      }]
    }
    metrics-server = {} # para o kubectl top da coleta
  }

  # gRPC entre células, API (k6 do cluster 1) e administração liberados dentro da VPC.
  node_security_group_additional_rules = {
    for nome, porta in var.portas_entre_clusters : "dr_${nome}" => {
      description = "DataReplication ${nome} dentro da VPC"
      protocol    = "tcp"
      from_port   = porta
      to_port     = porta
      type        = "ingress"
      cidr_blocks = [var.vpc_cidr]
    }
  }

  eks_managed_node_groups = merge(
    {
      cell = {
        ami_type                   = "AL2023_ARM_64_STANDARD"
        instance_types             = [var.tipo_no_celula]
        capacity_type              = "ON_DEMAND"
        min_size                   = 1
        max_size                   = 1
        desired_size               = 1
        use_custom_launch_template = false
        disk_size                  = 20
        labels                     = { "dr/role" = "cell" }
        # Sem taint: nos clusters 2 e 3 este é o único nó, e CoreDNS, EBS CSI,
        # ExternalDNS e metrics-server precisam rodar nele.
      }
    },
    each.key == "1" ? {
      k6 = {
        ami_type                   = "AL2023_ARM_64_STANDARD"
        instance_types             = var.tipos_no_k6
        capacity_type              = "SPOT"
        min_size                   = 1
        max_size                   = 1
        desired_size               = 1
        use_custom_launch_template = false
        disk_size                  = 20
        labels                     = { "dr/role" = "k6" }
        taints = {
          k6 = { key = "dr/role", value = "k6", effect = "NO_SCHEDULE" }
        }
      }
    } : {}
  )
}

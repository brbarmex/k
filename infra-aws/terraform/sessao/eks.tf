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

  # Quem roda o apply vira admin do cluster. Outros (role do SSO, outro usuário) entram
  # por var.admins_eks, também como cluster admin.
  enable_cluster_creator_admin_permissions = true
  access_entries = {
    for i, arn in var.admins_eks : "admin_${i}" => {
      principal_arn = arn
      policy_associations = {
        admin = {
          policy_arn   = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
          access_scope = { type = "cluster" }
        }
      }
    }
  }

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
    kube-proxy = {}
    # Uma réplica e request menor: nos clusters 2 e 3 tudo divide o único nó com a célula
    # (Guaranteed de 1,5 CPU), e a soma dos requests tem de caber nos ~1,93 vCPU alocáveis.
    coredns = {
      configuration_values = jsonencode({
        replicaCount = 1
        resources    = { requests = { cpu = "50m", memory = "70Mi" }, limits = { memory = "170Mi" } }
      })
    }
    eks-pod-identity-agent = { before_compute = true }
    aws-ebs-csi-driver = {
      configuration_values = jsonencode({ controller = { replicaCount = 1 } })
      pod_identity_association = [{
        role_arn        = aws_iam_role.ebs_csi.arn
        service_account = "ebs-csi-controller-sa"
      }]
    }
    metrics-server = {} # para o kubectl top da coleta
  }

  # Portas da POC e dos exportadores liberadas dentro da VPC (var.portas_entre_clusters).
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
      # Prometheus fora dos nós das células, para não disputar CPU com o app.
      monitoramento = {
        ami_type                   = "AL2023_ARM_64_STANDARD"
        instance_types             = [var.tipo_no_monitoramento]
        capacity_type              = "ON_DEMAND"
        min_size                   = 1
        max_size                   = 1
        desired_size               = 1
        use_custom_launch_template = false
        disk_size                  = 20
        labels                     = { "dr/role" = "monitoramento" }
        taints = {
          monitoramento = { key = "dr/role", value = "monitoramento", effect = "NO_SCHEDULE" }
        }
      }
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

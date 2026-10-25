# Portas da POC e dos exportadores no security group PRIMÁRIO de cada cluster.
# Com use_custom_launch_template = false, o EKS anexa aos nós só esse SG; o SG de nós do
# módulo (node_security_group_additional_rules) é criado mas não chega às instâncias.
locals {
  regras_entre_clusters = merge([
    for cel in keys(var.celulas) : {
      for nome, porta in var.portas_entre_clusters : "${cel}_${nome}" => { celula = cel, porta = porta }
    }
  ]...)
}

resource "aws_vpc_security_group_ingress_rule" "entre_clusters" {
  for_each = local.regras_entre_clusters

  security_group_id = module.eks[each.value.celula].cluster_primary_security_group_id
  description       = "DataReplication ${each.key} dentro da VPC"
  ip_protocol       = "tcp"
  from_port         = each.value.porta
  to_port           = each.value.porta
  cidr_ipv4         = var.vpc_cidr
}

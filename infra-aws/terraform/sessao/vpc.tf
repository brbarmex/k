# Só subnets públicas e sem NAT Gateway (~US$ 0,28/h em SP evitados). O nó usa o IP
# público só para saída; o security group dos nós não aceita entrada da internet.

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 6.0"

  name = var.projeto
  cidr = var.vpc_cidr

  azs             = values(var.celulas)
  public_subnets  = [for i, _ in values(var.celulas) : cidrsubnet(var.vpc_cidr, 4, i)]
  private_subnets = []

  enable_nat_gateway      = false
  map_public_ip_on_launch = true
  enable_dns_hostnames    = true # a zona privada do Route 53 exige DNS ligado na VPC
  enable_dns_support      = true
}

# Endpoint gateway do DynamoDB: grátis e mantém o tráfego do banco dentro da AWS.
resource "aws_vpc_endpoint" "dynamodb" {
  vpc_id            = module.vpc.vpc_id
  service_name      = "com.amazonaws.${var.regiao}.dynamodb"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = module.vpc.public_route_table_ids
}

locals {
  # Subnet pública de cada célula, pela AZ.
  subnet_da_celula = { for cell, az in var.celulas : cell => module.vpc.public_subnets[index(values(var.celulas), az)] }
}

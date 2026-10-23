# Zona privada onde o ExternalDNS de cada cluster publica a sua célula
# (cell-N-0.cell-N.dr.internal). Grátis se apagada em até 12 h. force_destroy porque o
# ExternalDNS deixa registros que o Terraform não conhece.
resource "aws_route53_zone" "privada" {
  name          = var.zona_privada
  force_destroy = true

  vpc {
    vpc_id = module.vpc.vpc_id
  }
}

output "regiao" {
  value = var.regiao
}

output "clusters" {
  description = "Cluster EKS de cada célula."
  value       = { for cell, m in module.eks : cell => m.cluster_name }
}

output "kubeconfig" {
  description = "Comandos para os contextos cluster-1..3 (o task aws:kubeconfig roda todos)."
  value = {
    for cell, m in module.eks : cell =>
    "aws eks update-kubeconfig --region ${var.regiao} --name ${m.cluster_name} --alias cluster-${cell}"
  }
}

output "tabelas" {
  value = { for cell, t in aws_dynamodb_table.cadastro : cell => t.name }
}

output "zona_privada_id" {
  value = aws_route53_zone.privada.zone_id
}

output "expira_em" {
  value = var.expira_em
}

# Uma tabela por célula, só com pk (decisão 3a, RF-05). On-demand: ~US$ 0,94 por milhão
# de escritas; cada escrita do cliente vira 3 PutItem (um por tabela).
resource "aws_dynamodb_table" "cadastro" {
  for_each = var.celulas

  name         = "cadastro_cliente_cell_${each.key}"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"

  attribute {
    name = "pk"
    type = "S"
  }

  deletion_protection_enabled = false # senão o destroy falha

  point_in_time_recovery {
    enabled = false # PITR cobra por GB
  }
}

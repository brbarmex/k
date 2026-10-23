# Stack "sessao": apply no início e destroy no fim de cada sessão de teste.
# Plano: /mnt/project-files/teste_aws_baixo_custo/ (02_terraform.md e 07_inventario_de_recursos.md).
terraform {
  required_version = ">= 1.9"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = var.regiao

  # expira-em é lida pela autodestruição da stack "conta". O task aws:up grava agora + TTL
  # e o task aws:estender adia (reaplica só as etiquetas).
  default_tags {
    tags = {
      projeto     = var.projeto
      stack       = "sessao"
      "expira-em" = var.expira_em
    }
  }
}

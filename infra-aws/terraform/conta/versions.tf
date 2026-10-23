# Stack "conta": aplicada uma vez e nunca destruída. Guarda os alarmes de custo e a
# autodestruição, que precisam sobreviver ao destroy da stack "sessao" (02_terraform.md, seção 1).
terraform {
  required_version = ">= 1.9"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.7"
    }
  }
}

provider "aws" {
  region = var.regiao

  default_tags {
    tags = {
      projeto = var.projeto
      stack   = "conta"
    }
  }
}

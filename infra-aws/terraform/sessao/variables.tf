variable "regiao" {
  description = "Região da POC (decisão 2a: sa-east-1)."
  type        = string
  default     = "sa-east-1"
}

variable "projeto" {
  description = "Etiqueta de tudo o que é da POC (autodestruição e aws:verificar)."
  type        = string
  default     = "dr-poc"
}

variable "expira_em" {
  description = "Instante UTC (RFC 3339) em que a autodestruição pode apagar a sessão. Preenchido pelo Taskfile."
  type        = string

  validation {
    condition     = can(regex("^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}Z$", var.expira_em))
    error_message = "Use o formato 2026-10-23T06:00:00Z (UTC)."
  }
}

variable "meu_ip_cidr" {
  description = "Seu IP público /32: único endereço que alcança a API dos 3 clusters EKS."
  type        = string

  validation {
    condition     = can(cidrhost(var.meu_ip_cidr, 0))
    error_message = "Informe um CIDR, por exemplo 200.1.2.3/32."
  }
}

variable "admins_eks" {
  description = "ARNs (usuário ou role IAM) que recebem cluster admin nos 3 clusters, além de quem roda o apply."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for a in var.admins_eks : can(regex("^arn:aws:iam::[0-9]{12}:(user|role)/", a))])
    error_message = "Use ARNs de usuário ou role IAM, por exemplo arn:aws:iam::123456789012:role/Admin."
  }
}

variable "kubernetes_version" {
  description = "Versão do Kubernetes. Use a mais nova em suporte padrão: em suporte estendido o EKS cobra US$ 0,60/h por cluster."
  type        = string
  default     = "1.34"
}

variable "celulas" {
  description = "Uma célula por AZ; cada célula é um cluster EKS (decisão 1b)."
  type        = map(string)
  default = {
    "1" = "sa-east-1a"
    "2" = "sa-east-1b"
    "3" = "sa-east-1c"
  }
}

variable "tipo_no_celula" {
  description = "Tipo do nó de cada célula (on-demand, decisão 7a). t4g.small não comporta os pods de sistema mais o app com 1 GiB."
  type        = string
  default     = "t4g.medium"
}

variable "tipos_no_k6" {
  description = "Tipos aceitos para o nó spot do k6 (vários tipos reduzem a chance de faltar capacidade)."
  type        = list(string)
  default     = ["t4g.small", "t4g.medium", "m7g.medium"]
}

variable "tipo_no_monitoramento" {
  description = "Tipo do nó dedicado ao Prometheus no cluster 1 (on-demand: uma interrupção spot levaria a observação no meio do teste)."
  type        = string
  default     = "t4g.medium"
}

variable "vpc_cidr" {
  description = "CIDR da VPC."
  type        = string
  default     = "10.20.0.0/16"
}

variable "zona_privada" {
  description = "Zona Route 53 privada onde o ExternalDNS publica as células (PEERS)."
  type        = string
  default     = "dr.internal"
}

variable "portas_entre_clusters" {
  description = "Portas liberadas dentro da VPC: gRPC entre células, API para o k6, administração e métricas do app, node-exporter e cAdvisor (scrape do Prometheus do cluster 1)."
  type        = map(number)
  default = {
    grpc          = 7000
    api           = 8000
    admin         = 9090
    node_exporter = 9100
    cadvisor      = 8085
  }
}

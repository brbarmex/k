variable "regiao" {
  description = "Região da POC (decisão 2a: sa-east-1)."
  type        = string
  default     = "sa-east-1"
}

variable "projeto" {
  description = "Etiqueta que marca tudo o que é da POC; a autodestruição e o aws:verificar procuram por ela."
  type        = string
  default     = "dr-poc"
}

variable "email_alertas" {
  description = "E-mail que recebe os alertas do Budgets, da detecção de anomalia e da autodestruição."
  type        = string
}

variable "orcamento_mensal_usd" {
  description = "Teto mensal de custo, em US$."
  type        = number
  default     = 15
}

variable "orcamento_diario_usd" {
  description = "Teto diário de custo, em US$: pega o 'esqueci ligado' no dia seguinte."
  type        = number
  default     = 3
}

variable "criar_monitor_de_anomalia" {
  description = "A conta aceita um único monitor de anomalia por serviço. Use false se já existir um."
  type        = bool
  default     = true
}

variable "anomalia_limite_usd" {
  description = "Impacto mínimo, em US$, para a detecção de anomalia avisar."
  type        = number
  default     = 2
}

variable "autodestruicao_intervalo" {
  description = "Frequência da varredura da autodestruição (expressão rate do EventBridge Scheduler)."
  type        = string
  default     = "rate(15 minutes)"
}

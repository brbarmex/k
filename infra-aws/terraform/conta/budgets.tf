# Budgets e detecção de anomalia: grátis. O Budgets atrasa de 8 a 24 h, por isso a
# autodestruição (autodestruicao.tf) é a primeira linha de defesa e o Budgets a segunda.

resource "aws_budgets_budget" "mensal" {
  name         = "${var.projeto}-mensal"
  budget_type  = "COST"
  limit_amount = tostring(var.orcamento_mensal_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 50
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.email_alertas]
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.email_alertas]
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"
    subscriber_email_addresses = [var.email_alertas]
  }
}

resource "aws_budgets_budget" "diario" {
  name         = "${var.projeto}-diario"
  budget_type  = "COST"
  limit_amount = tostring(var.orcamento_diario_usd)
  limit_unit   = "USD"
  time_unit    = "DAILY"

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.email_alertas]
  }
}

resource "aws_ce_anomaly_monitor" "servicos" {
  count = var.criar_monitor_de_anomalia ? 1 : 0

  name              = "${var.projeto}-servicos"
  monitor_type      = "DIMENSIONAL"
  monitor_dimension = "SERVICE"
}

resource "aws_ce_anomaly_subscription" "diaria" {
  count = var.criar_monitor_de_anomalia ? 1 : 0

  name             = "${var.projeto}-anomalia"
  frequency        = "DAILY"
  monitor_arn_list = [aws_ce_anomaly_monitor.servicos[0].arn]

  subscriber {
    type    = "EMAIL"
    address = var.email_alertas
  }

  threshold_expression {
    dimension {
      key           = "ANOMALY_TOTAL_IMPACT_ABSOLUTE"
      match_options = ["GREATER_THAN_OR_EQUAL"]
      values        = [tostring(var.anomalia_limite_usd)]
    }
  }
}

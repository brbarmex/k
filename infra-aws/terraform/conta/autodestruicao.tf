# Autodestruição (02_terraform.md, seção 6). Uma Lambda roda a cada 15 min e, para tudo
# que tem a etiqueta projeto = dr-poc e a etiqueta expira-em no passado:
#   1. apaga os node groups (para o custo de EC2);
#   2. apaga o cluster EKS quando ele não tem mais node group;
#   3. apaga as tabelas DynamoDB;
#   4. apaga volumes EBS soltos (status available) com a etiqueta, só quando não existe
#      mais nenhum cluster da POC (um volume pode ficar solto por instantes durante a
#      troca de nó do FP-08).
# A stack "sessao" grava expira-em = agora + TTL (task aws:up) e o task aws:estender adia.

data "aws_caller_identity" "atual" {}

resource "aws_sns_topic" "alertas" {
  name = "${var.projeto}-alertas"
}

resource "aws_sns_topic_subscription" "email" {
  topic_arn = aws_sns_topic.alertas.arn
  protocol  = "email"
  endpoint  = var.email_alertas
}

data "archive_file" "autodestruicao" {
  type        = "zip"
  source_file = "${path.module}/lambda/autodestruicao.py"
  output_path = "${path.module}/.build/autodestruicao.zip"
}

resource "aws_iam_role" "autodestruicao" {
  name = "${var.projeto}-autodestruicao"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "autodestruicao" {
  name = "autodestruicao"
  role = aws_iam_role.autodestruicao.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "eks:ListClusters",
          "eks:DescribeCluster",
          "eks:ListNodegroups",
          "eks:DeleteNodegroup",
          "eks:DeleteCluster",
          "dynamodb:ListTables",
          "dynamodb:DescribeTable",
          "dynamodb:ListTagsOfResource",
          "dynamodb:DeleteTable",
          "ec2:DescribeVolumes",
          "ec2:DeleteVolume",
        ]
        Resource = "*"
      },
      {
        Effect   = "Allow"
        Action   = "sns:Publish"
        Resource = aws_sns_topic.alertas.arn
      },
      {
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = "${aws_cloudwatch_log_group.autodestruicao.arn}:*"
      },
    ]
  })
}

# Log group criado aqui para ter retenção curta (o padrão da Lambda é guardar para sempre).
resource "aws_cloudwatch_log_group" "autodestruicao" {
  name              = "/aws/lambda/${var.projeto}-autodestruicao"
  retention_in_days = 7
}

resource "aws_lambda_function" "autodestruicao" {
  function_name    = "${var.projeto}-autodestruicao"
  role             = aws_iam_role.autodestruicao.arn
  runtime          = "python3.12"
  handler          = "autodestruicao.handler"
  filename         = data.archive_file.autodestruicao.output_path
  source_code_hash = data.archive_file.autodestruicao.output_base64sha256
  timeout          = 300
  memory_size      = 128
  architectures    = ["arm64"]

  environment {
    variables = {
      PROJETO   = var.projeto
      TOPICO    = aws_sns_topic.alertas.arn
      SIMULACAO = "false" # "true" só registra o que apagaria
    }
  }

  depends_on = [aws_cloudwatch_log_group.autodestruicao]
}

resource "aws_iam_role" "agendador" {
  name = "${var.projeto}-agendador"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "scheduler.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = { StringEquals = { "aws:SourceAccount" = data.aws_caller_identity.atual.account_id } }
    }]
  })
}

resource "aws_iam_role_policy" "agendador" {
  name = "invocar-autodestruicao"
  role = aws_iam_role.agendador.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "lambda:InvokeFunction"
      Resource = aws_lambda_function.autodestruicao.arn
    }]
  })
}

resource "aws_scheduler_schedule" "autodestruicao" {
  name                = "${var.projeto}-autodestruicao"
  schedule_expression = var.autodestruicao_intervalo

  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.autodestruicao.arn
    role_arn = aws_iam_role.agendador.arn
  }
}

output "lambda_autodestruicao" {
  description = "Nome da Lambda; para testar sem apagar nada: SIMULACAO=true e aws lambda invoke."
  value       = aws_lambda_function.autodestruicao.function_name
}

output "topico_alertas" {
  description = "Tópico SNS dos avisos. Confirme a assinatura no e-mail recebido depois do apply."
  value       = aws_sns_topic.alertas.arn
}

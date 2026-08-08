output "alb_dns_name" {
  value = aws_lb.main.dns_name
}

output "app_url" {
  value = var.domain_name != "" ? "https://${var.domain_name}" : "http://${aws_lb.main.dns_name}"
}

output "ecr_repository_url" {
  value = aws_ecr_repository.app.repository_url
}

output "secrets_manager_arn" {
  value = aws_secretsmanager_secret.app.arn
}

output "ecs_cluster_name" {
  value = aws_ecs_cluster.main.name
}

output "ecs_service_name" {
  value = aws_ecs_service.app.name
}

output "github_actions_role_arn" {
  description = "Set this as the role-to-assume in the GHA workflow's configure-aws-credentials step."
  value       = aws_iam_role.github_actions.arn
}

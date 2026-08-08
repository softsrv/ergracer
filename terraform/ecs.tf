resource "aws_ecs_cluster" "main" {
  name = "${var.project_name}-cluster"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_cloudwatch_log_group" "app" {
  name              = "/ecs/${var.project_name}"
  retention_in_days = 30
}

resource "aws_security_group" "ecs_tasks" {
  name        = "${var.project_name}-ecs-tasks"
  description = "Allow inbound from the ALB only"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "From ALB"
    from_port       = var.container_port
    to_port         = var.container_port
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.project_name}-ecs-tasks-sg" }
}

locals {
  # Non-sensitive env vars, passed straight through in the task definition.
  # Keep anything secret (tokens, connection strings, credentials) out of
  # this map — it ends up readable in the task definition's plaintext JSON,
  # unlike the `secrets` block below.
  plain_env = {
    APP_ENV                 = var.environment
    PORT                    = tostring(var.container_port)
    APP_BASE_URL            = var.app_base_url
    DB_MAX_CONNS            = "25"
    JWT_ACCESS_EXPIRY       = "15m"
    REFRESH_TOKEN_EXPIRY    = "120h"
    BCRYPT_COST             = "12"
    PASSWORD_MIN_LENGTH     = "8"
    DISCORD_BOT_PERMISSIONS = "0"
    CONCEPT2_API_BASE       = "https://log.concept2.com"
    TRUSTED_PROXY_COUNT     = tostring(var.trusted_proxy_count)
  }

  # Sensitive keys, pulled individually from the single JSON secret in
  # secrets.tf via ECS's "<secret-arn>:<jsonKey>::" reference syntax. Must
  # match the key set the secret is expected to contain — see secrets.tf.
  secret_keys = [
    "DATABASE_URL",
    "JWT_SECRET",
    "OAUTH_TOKEN_ENC_KEY",
    "DISCORD_APPLICATION_ID",
    "DISCORD_CLIENT_ID",
    "DISCORD_CLIENT_SECRET",
    "DISCORD_PUBLIC_KEY",
    "DISCORD_BOT_TOKEN",
    "CONCEPT2_CLIENT_ID",
    "CONCEPT2_CLIENT_SECRET",
  ]
}

resource "aws_ecs_task_definition" "app" {
  family                   = var.project_name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.fargate_cpu
  memory                   = var.fargate_memory
  execution_role_arn       = aws_iam_role.ecs_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([
    {
      name      = var.project_name
      image     = "${aws_ecr_repository.app.repository_url}:${var.image_tag}"
      essential = true

      portMappings = [
        {
          containerPort = var.container_port
          protocol      = "tcp"
        }
      ]

      environment = [
        for k, v in local.plain_env : { name = k, value = v }
      ]

      secrets = [
        for k in local.secret_keys : {
          name      = k
          valueFrom = "${aws_secretsmanager_secret.app.arn}:${k}::"
        }
      ]

      # No container-level healthCheck: the runtime image is
      # gcr.io/distroless/static (see Dockerfile), which has no shell —
      # ECS's CMD-SHELL-based health check has nothing to execute. The ALB
      # target group's HTTP health check against /health (alb.tf) is the
      # only liveness signal, and health_check_grace_period_seconds below
      # gives the task time to pass it before ECS considers a slow start a
      # failure.
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.app.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "app"
        }
      }
    }
  ])
}

resource "aws_ecs_service" "app" {
  name            = var.project_name
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.app.arn
  desired_count   = var.desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.ecs_tasks.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.app.arn
    container_name   = var.project_name
    container_port   = var.container_port
  }

  health_check_grace_period_seconds = 30

  depends_on = [aws_lb_listener.http]
}

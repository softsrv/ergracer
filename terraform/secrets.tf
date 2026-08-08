# One JSON secret holding every sensitive .env value. ecs.tf's task
# definition maps each key back out to its own env var via the ECS `secrets`
# field's "<arn>:<jsonKey>::" syntax, so the app itself needs no changes —
# it still just reads individual env vars with os.Getenv.
resource "aws_secretsmanager_secret" "app" {
  name        = "${var.project_name}/${var.environment}/app-env"
  description = "Sensitive RowBot env vars. Populate/update with: aws secretsmanager put-secret-value --secret-id ${var.project_name}/${var.environment}/app-env --secret-string file://secrets.json"
}

# Seeds one version so the task definition has something to reference on the
# very first apply. Real values are populated out-of-band via the AWS CLI or
# console (never through a committed .tfvars file) — ignore_changes keeps
# Terraform from ever overwriting them with this placeholder again, and keeps
# real secret values out of `terraform plan` diffs.
resource "aws_secretsmanager_secret_version" "app" {
  secret_id     = aws_secretsmanager_secret.app.id
  secret_string = jsonencode({ for k in local.secret_keys : k => "REPLACE_ME" })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

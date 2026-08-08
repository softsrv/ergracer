variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "project_name" {
  type    = string
  default = "rowbot"
}

variable "environment" {
  type    = string
  default = "production"
}

variable "vpc_cidr" {
  type    = string
  default = "10.20.0.0/16"
}

variable "az_count" {
  type    = number
  default = 2
}

variable "container_port" {
  type    = number
  default = 8080
}

variable "fargate_cpu" {
  description = "Fargate task vCPU units (256 = 0.25 vCPU). See AWS docs for valid cpu/memory pairs."
  type        = number
  default     = 256
}

variable "fargate_memory" {
  description = "Fargate task memory in MiB."
  type        = number
  default     = 512
}

variable "desired_count" {
  type    = number
  default = 1
}

variable "image_tag" {
  description = "Docker image tag to deploy, e.g. v1.0.0. No default on purpose — pick a tag explicitly for every prod apply rather than drifting to whatever 'latest' happens to be."
  type        = string
}

variable "domain_name" {
  description = "Optional custom domain (e.g. rowbot.example.com). When set, provisions an ACM cert + Route53 record and adds an HTTPS listener on the ALB, with HTTP redirecting to it. Leave empty to serve the app over plain HTTP on the ALB's default *.elb.amazonaws.com hostname."
  type        = string
  default     = ""
}

variable "route53_zone_id" {
  description = "Existing Route53 hosted zone ID for domain_name. Required when domain_name is set."
  type        = string
  default     = ""
}

variable "app_base_url" {
  description = "Public URL the app is served at (scheme + host, no trailing slash) — becomes APP_BASE_URL. Used for OAuth redirect URIs and links in emails, so it must exactly match what's actually reachable: the ALB DNS name over http:// when domain_name is unset, or https://<domain_name> once it's set."
  type        = string
}

variable "trusted_proxy_count" {
  description = "Number of trusted reverse-proxy hops in front of the app, for correct client-IP extraction (see middleware.ClientIP). 1 for a single ALB in front of Fargate."
  type        = number
  default     = 1
}

variable "github_repository" {
  description = "GitHub repo allowed to assume the CI deploy role, as \"owner/repo\". Required for the OIDC trust policy in github_oidc.tf."
  type        = string
}

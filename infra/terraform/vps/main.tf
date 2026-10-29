# A single Linux VPS with root SSH access (a Bluehost VPS or dedicated server,
# Hetzner, DigitalOcean, Lightsail, ...) running the stack with Docker Compose
# behind Caddy, which gets the HTTPS certificates. See infra/vps/.
#
# There are no cloud resources and no providers here. This root turns
# secret.tfvars into the server's .env file and tells the deploy playbook how
# to reach the server and which DNS records point the domain at it. Its state
# holds the rendered .env, so keep it in the same encrypted backend as the
# other roots.
terraform {
  required_version = ">= 1.9"
  # backend "gcs" {
  #   bucket = "cadentic-tfstate"
  #   prefix = "portfolio-cms/vps"
  # }
}

variable "vps_ipv4" {
  type        = string
  default     = ""
  description = "Public IPv4 address of the server. The site host's A record points here."
}
variable "vps_ipv6" {
  type        = string
  default     = ""
  description = "Public IPv6 address, if the server has one (adds an AAAA record)."
  validation {
    condition     = var.vps_ipv6 == "" || (strcontains(var.vps_ipv6, ":") && can(cidrhost("${var.vps_ipv6}/128", 0)))
    error_message = "vps_ipv6 must be the server's IPv6 address, such as 2001:db8::10, or empty."
  }
}
variable "vps_ssh_host" {
  type        = string
  default     = ""
  description = "Address used for SSH. Defaults to vps_ipv4."
}
variable "vps_ssh_user" {
  type    = string
  default = "root"
}
variable "vps_ssh_port" {
  type    = number
  default = 22
}
variable "vps_dir" {
  type        = string
  default     = "/opt/portfolio"
  description = "Directory on the server that holds compose.yml, Caddyfile and .env."
}
variable "vps_platform" {
  type        = string
  default     = "linux/amd64"
  description = "CPU platform of the server's images: linux/amd64, or linux/arm64 for ARM servers."
}
variable "vps_local_db" {
  type        = bool
  default     = false
  description = "Run PostgreSQL 18 in a container on the server instead of using database_url (Neon)."
}
variable "vps_db_password" {
  type        = string
  default     = ""
  sensitive   = true
  description = "Password for the server's PostgreSQL container (vps_local_db = true), e.g. `openssl rand -hex 24`."
}

locals {
  ssh_host = var.vps_ssh_host != "" ? var.vps_ssh_host : var.vps_ipv4
  database_url = (var.vps_local_db
    ? "postgres://portfolio:${var.vps_db_password}@db:5432/portfolio?sslmode=disable"
  : var.database_url)

  # Values for compose.yml. Everything sensitive stays in this file on the
  # server (mode 0600) and is passed only to the containers that need it.
  env = merge(local.media_env, {
    COMPOSE_PROFILES             = var.vps_local_db ? "localdb" : ""
    DOMAIN                       = var.domain
    SITE_HOSTS                   = join(",", local.site_hosts)
    SITE_ADDRESSES               = join(" ", local.site_hosts)
    PUBLIC_SITE_URL              = local.site_url
    DATABASE_URL                 = local.database_url
    POSTGRES_PASSWORD            = var.vps_local_db ? var.vps_db_password : ""
    APP_SECRET                   = var.app_secret
    FIREBASE_PROJECT_ID          = var.firebase_project_id
    FIREBASE_API_KEY             = var.firebase_api_key
    FIREBASE_AUTH_DOMAIN         = var.firebase_auth_domain
    ADMIN_EMAILS                 = var.admin_emails
    ADMIN_UIDS                   = var.admin_uids
    ADMIN_ALERT_EMAIL            = var.admin_alert_email
    EMAIL_FROM_ADDRESS           = var.email_from_address
    EMAIL_BREVO_API_KEY          = var.email_brevo_api_key
    EMAIL_BREVO_WEBHOOK_SECRET   = var.email_brevo_webhook_secret
    EMAIL_MAILJET_API_KEY        = var.email_mailjet_api_key
    EMAIL_MAILJET_SECRET_KEY     = var.email_mailjet_secret_key
    EMAIL_MAILJET_WEBHOOK_SECRET = var.email_mailjet_webhook_secret
    GTM_ID                       = var.gtm_id
    GA4_MEASUREMENT_ID           = var.ga4_measurement_id
    # A bucket outside the server is reached with a key limited to it.
    MEDIA_GCS_CREDENTIALS = var.media_storage == "gcs" ? var.media_gcs_credentials : ""
    MEDIA_AZURE_SAS       = var.media_storage == "azure" ? var.media_azure_sas : ""
  })

  site_records = concat(
    [{ name = local.site_host, type = "A", content = var.vps_ipv4 }],
    var.vps_ipv6 != "" ? [{ name = local.site_host, type = "AAAA", content = var.vps_ipv6 }] : [],
    [for h in local.site_hosts : { name = h, type = "CNAME", content = local.site_host } if h != local.site_host],
  )
}

# Compose reads single-quoted .env values literally, so a value must not
# contain a single quote or a line break.
output "env_file" {
  sensitive = true
  value     = join("", [for k in sort(keys(local.env)) : "${k}='${local.env[k]}'\n"])
  precondition {
    condition     = alltrue([for v in values(local.env) : !strcontains(v, "'") && !strcontains(v, "\n")])
    error_message = "A value in secret.tfvars contains a single quote or a line break, which the server's .env file cannot hold. Change that value."
  }
  precondition {
    condition     = var.media_storage == "local" || nonsensitive(local.env.MEDIA_GCS_CREDENTIALS != "" || local.env.MEDIA_AZURE_SAS != "")
    error_message = "A media bucket on a VPS needs a key for it: set media_access_key = true in secret.tfvars and deploy with the playbook, which passes the key in."
  }
  precondition {
    condition     = can(regex("^[0-9]{1,3}(\\.[0-9]{1,3}){3}$", var.vps_ipv4)) && can(cidrnetmask("${var.vps_ipv4}/32"))
    error_message = "Set vps_ipv4 in secret.tfvars to the server's public IPv4 address."
  }
  precondition {
    condition     = !var.vps_local_db || can(regex("^[A-Za-z0-9._~-]{16,}$", var.vps_db_password))
    error_message = "vps_local_db = true needs vps_db_password: 16+ characters from A-Z a-z 0-9 . _ ~ - (for example `openssl rand -hex 24`)."
  }
}

output "ssh" {
  value = { host = local.ssh_host, user = var.vps_ssh_user, port = var.vps_ssh_port, dir = var.vps_dir }
}
output "platform" { value = var.vps_platform }
output "local_db" { value = var.vps_local_db }
output "site_url" { value = local.site_url }
output "web_url" { value = local.site_url }
output "site_records" { value = local.site_records }
output "webhook_urls" {
  value = { brevo = "${local.site_url}/api/webhooks/brevo", mailjet = "${local.site_url}/api/webhooks/mailjet" }
}

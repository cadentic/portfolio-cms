# Variables shared by the gcp/ and azure/ roots (each links this file).
# Values come from secret.tfvars (git-ignored); see secret.tfvars.example.

variable "domain" {
  type        = string
  description = "Public site domain, also used as the sending domain."
  default     = "cadentic.dev"
}

variable "api_image" {
  type        = string
  description = "API/worker image reference. Set by the Ansible deploy playbook."
  default     = ""
}

variable "web_image" {
  type        = string
  description = "Web (Angular SSR) image reference. Set by the Ansible deploy playbook."
  default     = ""
}

# --- application secrets -------------------------------------------------------

variable "database_url" {
  type        = string
  sensitive   = true
  description = "Neon Postgres connection string (postgresql://...?sslmode=require). Create the database in the Neon console; the API runs migrations itself."
}

variable "app_secret" {
  type        = string
  sensitive   = true
  description = "At least 32 random characters. Signs subscription and unsubscribe tokens."
  validation {
    condition     = length(var.app_secret) >= 32
    error_message = "app_secret must be at least 32 characters."
  }
}

variable "admin_emails" {
  type        = string
  description = "Comma-separated Google/Firebase account emails allowed into /admin."
}

variable "admin_uids" {
  type        = string
  default     = ""
  description = "Optional comma-separated Firebase UIDs allowed into /admin (stricter than emails)."
}

variable "admin_alert_email" {
  type        = string
  default     = ""
  description = "Where delivery outage and dead-letter alerts are sent. Not published anywhere."
}

variable "firebase_project_id" {
  type = string
}

variable "firebase_api_key" {
  type        = string
  description = "Firebase Web API key. Public by design (it ships to browsers); kept here for convenience."
}

variable "firebase_auth_domain" {
  type    = string
  default = ""
}

variable "email_from_address" {
  type    = string
  default = "newsletter@cadentic.dev"
}

variable "email_brevo_api_key" {
  type      = string
  sensitive = true
}

variable "email_brevo_webhook_secret" {
  type      = string
  sensitive = true
}

variable "email_mailjet_api_key" {
  type      = string
  sensitive = true
}

variable "email_mailjet_secret_key" {
  type      = string
  sensitive = true
}

variable "email_mailjet_webhook_secret" {
  type      = string
  sensitive = true
}

variable "gtm_id" {
  type    = string
  default = ""
}

variable "ga4_measurement_id" {
  type    = string
  default = ""
}

# --- Cloudflare DNS ------------------------------------------------------------

variable "cloudflare_api_token" {
  type        = string
  sensitive   = true
  description = "Token with Zone:DNS:Edit on the domain's zone."
}

variable "cloudflare_zone_id" {
  type = string
}

variable "email_dns_records" {
  description = "Provider-specific mail records from the Brevo and Mailjet dashboards (DKIM CNAME/TXT, brevo-code TXT, mailjet TXT)."
  type = list(object({
    name    = string
    type    = string
    content = string
  }))
  default = []
}

variable "dmarc_policy" {
  type        = string
  default     = "quarantine"
  description = "Start with none or quarantine; move to reject once reports look clean."
}

variable "dmarc_rua" {
  type        = string
  default     = ""
  description = "Mailbox for DMARC aggregate reports, e.g. dmarc@cadentic.dev. Empty disables reports."
}

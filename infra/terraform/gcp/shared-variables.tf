# Variables shared by every root. gcp/, azure/, vps/, dns/, media-gcs/ and
# media-azure/ each hold an identical copy (a symlink breaks on Windows).
# Edit this file, then run scripts/sync-terraform-vars.sh (CI checks the copies).
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
  description = "Neon Postgres direct connection string (postgresql://...?sslmode=require, no -pooler in the host). Create the database in the Neon console; the API runs migrations itself."
}

variable "app_secret" {
  type        = string
  sensitive   = true
  description = "At least 32 random characters. Signs subscription and unsubscribe tokens."
  validation {
    condition     = length(var.app_secret) >= 32 && !startswith(var.app_secret, "REPLACE_")
    error_message = "app_secret must be at least 32 random characters; replace the example value (e.g. `openssl rand -base64 48`)."
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
  validation {
    condition     = !startswith(var.email_brevo_webhook_secret, "REPLACE_")
    error_message = "email_brevo_webhook_secret still holds the example value; set a long random string."
  }
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
  validation {
    condition     = !startswith(var.email_mailjet_webhook_secret, "REPLACE_")
    error_message = "email_mailjet_webhook_secret still holds the example value; set a long random string."
  }
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

# --- media (uploads and the imported WordPress images) ------------------------
# The bucket is private: visitors get files through the site (/media/...), and
# only the API's own identity, or a key limited to this one bucket, can read or
# write it. media-gcs/ and media-azure/ create the bucket; the deploy playbook
# applies the one media_storage names before the site.

variable "media_storage" {
  type        = string
  default     = "local"
  description = "Where the site keeps media: local (a volume on the server; VPS only), gcs (a Google Cloud Storage bucket) or azure (an Azure Blob container)."
  validation {
    condition     = contains(["local", "gcs", "azure"], var.media_storage)
    error_message = "media_storage must be local, gcs or azure."
  }
  validation {
    condition     = var.media_storage != "gcs" || var.media_gcs_bucket != ""
    error_message = "media_storage = \"gcs\" needs media_gcs_bucket."
  }
  validation {
    condition     = var.media_storage != "azure" || var.media_azure_account != ""
    error_message = "media_storage = \"azure\" needs media_azure_account."
  }
}

variable "media_access_key" {
  type        = bool
  default     = false
  description = "Create a key limited to the bucket (a service-account key for Google, a container SAS token for Azure) for a site outside the bucket's cloud: a VPS, Azure with a Google bucket, or Cloud Run with an Azure container. Keep it on until a move between clouds is finished."
}

variable "media_gcs_project_id" {
  type        = string
  default     = ""
  description = "Google Cloud project that holds the bucket. It can differ from gcp_project_id; empty means gcp_project_id."
}

variable "media_gcs_bucket" {
  type        = string
  default     = ""
  description = "Globally unique bucket name, e.g. cadentic-portfolio-media."
  validation {
    condition     = var.media_gcs_bucket == "" || can(regex("^[a-z0-9][a-z0-9_-]{1,61}[a-z0-9]$", var.media_gcs_bucket))
    error_message = "media_gcs_bucket: 3-63 lowercase letters, digits, - and _ (no dots)."
  }
}

variable "media_gcs_location" {
  type        = string
  default     = "asia-southeast1"
  description = "Bucket region. Use the Cloud Run region to avoid transfer charges."
}

variable "media_azure_subscription_id" {
  type        = string
  default     = ""
  description = "Azure subscription that holds the storage account. It can differ from azure_subscription_id (same Entra tenant); empty means azure_subscription_id."
}

variable "media_azure_resource_group" {
  type    = string
  default = "rg-portfolio-media"
}

variable "media_azure_location" {
  type    = string
  default = "centralindia"
}

variable "media_azure_account" {
  type        = string
  default     = ""
  description = "Globally unique storage account name, 3-24 lowercase letters and digits, e.g. cadenticmedia."
  validation {
    condition     = var.media_azure_account == "" || can(regex("^[a-z0-9]{3,24}$", var.media_azure_account))
    error_message = "media_azure_account: 3-24 lowercase letters and digits."
  }
}

variable "media_azure_container" {
  type    = string
  default = "media"
  validation {
    condition     = can(regex("^[a-z0-9](-?[a-z0-9])+$", var.media_azure_container)) && length(var.media_azure_container) >= 3 && length(var.media_azure_container) <= 63
    error_message = "media_azure_container: 3-63 lowercase letters, digits and single hyphens, starting and ending with a letter or digit."
  }
}

# Set by the deploy playbook from the media root's outputs (media.auto.tfvars.json).
variable "media_gcs_credentials" {
  type      = string
  default   = ""
  sensitive = true
}

variable "media_azure_sas" {
  type      = string
  default   = ""
  sensitive = true
}

locals {
  # The API's media settings (gcp/, azure/ and vps/ add the key, if any).
  media_env = merge(
    { MEDIA_STORAGE = var.media_storage },
    var.media_storage == "gcs" ? { MEDIA_GCS_BUCKET = var.media_gcs_bucket } : {},
    var.media_storage == "azure" ? { MEDIA_AZURE_ACCOUNT = var.media_azure_account, MEDIA_AZURE_CONTAINER = var.media_azure_container } : {},
  )
}

# Cloudflare DNS for the domain: the records that point the site at whichever
# host serves it now (Cloud Run, Container Apps or a VPS), plus SPF, DMARC and
# the Brevo and Mailjet records. It has its own state, separate from the
# gcp/, azure/ and vps/ roots, so moving the site between hosts only edits
# these records and two roots never own the same record.
#
# The deploy playbook applies it with the active target's site_records output:
#   ansible-playbook deploy.yml -e target=gcp|azure|vps
terraform {
  required_version = ">= 1.9"
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 5.0" }
  }
  # State holds the Cloudflare token. Use an encrypted remote backend, for example:
  # backend "gcs" {
  #   bucket = "cadentic-tfstate"
  #   prefix = "portfolio-cms/dns"
  # }
}

variable "site_target" {
  type        = string
  description = "Which deployment the domain points at. Set by the deploy playbook."
  validation {
    condition     = contains(["gcp", "azure", "vps"], var.site_target)
    error_message = "site_target must be gcp, azure or vps."
  }
}

variable "site_records" {
  type        = list(object({ name = string, type = string, content = string }))
  description = "The site_records output of the target root. Set by the deploy playbook; no default, so a bare apply cannot remove the site records."
}

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

module "dns" {
  source            = "../modules/cloudflare-dns"
  zone_id           = var.cloudflare_zone_id
  domain            = var.domain
  site_records      = var.site_records
  email_dns_records = var.email_dns_records
  dmarc_policy      = var.dmarc_policy
  dmarc_rua         = var.dmarc_rua
}

output "site_target" { value = var.site_target }

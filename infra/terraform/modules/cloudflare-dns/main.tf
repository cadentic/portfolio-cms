# Site records (pointing at Cloud Run, Container Apps or a VPS) plus SPF,
# DMARC and the provider DKIM records for Brevo and Mailjet.
terraform {
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 5.0" }
  }
}

variable "zone_id" { type = string }
variable "domain" { type = string }
variable "site_records" {
  description = "Records that point the site at its host. DNS-only: the managed certificates (Cloud Run, Container Apps, Caddy on a VPS) are validated over plain DNS."
  type        = list(object({ name = string, type = string, content = string }))
}
variable "email_dns_records" {
  type = list(object({ name = string, type = string, content = string }))
}
variable "dmarc_policy" { type = string }
variable "dmarc_rua" { type = string }

locals {
  # Site records are keyed by type, name and position among the records with
  # that type and name. Moving the site to another host then edits records in
  # place (the www CNAME keeps its key) instead of deleting and re-creating
  # them, which Cloudflare would reject while the old record still exists.
  site_groups = distinct([for r in var.site_records : "${r.type}:${r.name}"])
  site = merge({}, [for g in local.site_groups : {
    for i, r in [for x in var.site_records : x if "${x.type}:${x.name}" == g] : "${g}:${i}" => r
  }]...)
  email = { for i, r in var.email_dns_records : "${r.type}:${r.name}:${i}" => r }
  dmarc = join("; ", compact(["v=DMARC1", "p=${var.dmarc_policy}", var.dmarc_rua != "" ? "rua=mailto:${var.dmarc_rua}" : "", "adkim=r", "aspf=r"]))
}

resource "cloudflare_dns_record" "site" {
  for_each = local.site
  zone_id  = var.zone_id
  name     = each.value.name
  type     = each.value.type
  content  = each.value.content
  proxied  = false
  ttl      = 300
  comment  = "portfolio-cms site (managed by Terraform)"
}

resource "cloudflare_dns_record" "spf" {
  zone_id = var.zone_id
  name    = var.domain
  type    = "TXT"
  content = "\"v=spf1 include:spf.brevo.com include:spf.mailjet.com ~all\""
  ttl     = 3600
  comment = "SPF for Brevo and Mailjet (managed by Terraform)"
}

resource "cloudflare_dns_record" "dmarc" {
  zone_id = var.zone_id
  name    = "_dmarc.${var.domain}"
  type    = "TXT"
  content = "\"${local.dmarc}\""
  ttl     = 3600
  comment = "DMARC (managed by Terraform)"
}

resource "cloudflare_dns_record" "email" {
  for_each = local.email
  zone_id  = var.zone_id
  name     = each.value.name
  type     = each.value.type
  content  = each.value.type == "TXT" ? "\"${trim(each.value.content, "\"")}\"" : each.value.content
  proxied  = false
  ttl      = 3600
  comment  = "Email provider record (managed by Terraform)"
}

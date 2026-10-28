# Azure Container Apps deployment: web (public, Angular SSR), api (internal
# ingress, reachable only from inside the environment) and worker (no ingress,
# always one replica). Database: Neon. DNS and mail records: Cloudflare.
terraform {
  required_version = ">= 1.9"
  required_providers {
    azurerm    = { source = "hashicorp/azurerm", version = ">= 4.0, < 6.0" }
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 5.0" }
  }
  # State holds secret values. Use an encrypted remote backend, for example:
  # backend "azurerm" { resource_group_name = "tfstate" storage_account_name = "cadentictfstate" container_name = "tfstate" key = "portfolio-cms.tfstate" }
}

variable "azure_subscription_id" { type = string }
variable "azure_location" {
  type    = string
  default = "centralindia"
}
variable "name" {
  type    = string
  default = "portfolio"
}
variable "acr_name" {
  type        = string
  default     = "cadenticportfolio"
  description = "Globally unique, 5-50 lowercase letters and digits."
}
variable "bind_domain" {
  type        = bool
  default     = true
  description = "Add cadentic.dev and www to the web app. The Ansible playbook then issues the managed certificates."
}

provider "azurerm" {
  features {}
  subscription_id = var.azure_subscription_id
}

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

locals {
  site_url = "https://${var.domain}"
  secrets = {
    "database-url"                 = var.database_url
    "app-secret"                   = var.app_secret
    "email-brevo-api-key"          = var.email_brevo_api_key
    "email-brevo-webhook-secret"   = var.email_brevo_webhook_secret
    "email-mailjet-api-key"        = var.email_mailjet_api_key
    "email-mailjet-secret-key"     = var.email_mailjet_secret_key
    "email-mailjet-webhook-secret" = var.email_mailjet_webhook_secret
  }
  api_env = {
    APP_ENV                = "production"
    PUBLIC_SITE_URL        = local.site_url
    CORS_ALLOWED_ORIGINS   = local.site_url
    TRUST_CLIENT_IP_HEADER = "true" # the API has internal ingress only
    FIREBASE_PROJECT_ID    = var.firebase_project_id
    ADMIN_EMAILS           = var.admin_emails
    ADMIN_UIDS             = var.admin_uids
    ADMIN_ALERT_EMAIL      = var.admin_alert_email
    EMAIL_FROM_ADDRESS     = var.email_from_address
    GTM_ID                 = var.gtm_id
    GA4_MEASUREMENT_ID     = var.ga4_measurement_id
  }
  hosts = var.bind_domain ? { apex = var.domain, www = "www.${var.domain}" } : {}
}

resource "azurerm_resource_group" "rg" {
  name     = "rg-${var.name}"
  location = var.azure_location
}

resource "azurerm_log_analytics_workspace" "logs" {
  name                = "log-${var.name}"
  location            = azurerm_resource_group.rg.location
  resource_group_name = azurerm_resource_group.rg.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
}

resource "azurerm_container_app_environment" "env" {
  name                       = "cae-${var.name}"
  location                   = azurerm_resource_group.rg.location
  resource_group_name        = azurerm_resource_group.rg.name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.logs.id
}

resource "azurerm_container_registry" "acr" {
  name                = var.acr_name
  resource_group_name = azurerm_resource_group.rg.name
  location            = azurerm_resource_group.rg.location
  sku                 = "Basic"
  admin_enabled       = false
}

resource "azurerm_user_assigned_identity" "apps" {
  name                = "id-${var.name}"
  resource_group_name = azurerm_resource_group.rg.name
  location            = azurerm_resource_group.rg.location
}

resource "azurerm_role_assignment" "pull" {
  scope                = azurerm_container_registry.acr.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_user_assigned_identity.apps.principal_id
}

# --- API and worker share an image and configuration -----------------------------

resource "azurerm_container_app" "api" {
  for_each                     = { api = false, worker = true }
  name                         = "${var.name}-${each.key}"
  container_app_environment_id = azurerm_container_app_environment.env.id
  resource_group_name          = azurerm_resource_group.rg.name
  revision_mode                = "Single"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.apps.id]
  }
  registry {
    server   = azurerm_container_registry.acr.login_server
    identity = azurerm_user_assigned_identity.apps.id
  }

  dynamic "secret" {
    for_each = nonsensitive(toset(keys(local.secrets)))
    content {
      name  = secret.value
      value = local.secrets[secret.value]
    }
  }

  dynamic "ingress" {
    for_each = each.value ? [] : [1]
    content {
      external_enabled = false
      target_port      = 8080
      transport        = "http"
      traffic_weight {
        latest_revision = true
        percentage      = 100
      }
    }
  }

  template {
    min_replicas = each.value ? 1 : 0
    max_replicas = each.value ? 2 : 4
    container {
      name   = each.key
      image  = var.api_image
      cpu    = 0.5
      memory = "1Gi"
      dynamic "env" {
        for_each = merge(local.api_env, { RUN_WORKER = each.value ? "true" : "false" })
        content {
          name  = env.key
          value = env.value
        }
      }
      dynamic "env" {
        for_each = nonsensitive(toset(keys(local.secrets)))
        content {
          name        = upper(replace(env.value, "-", "_"))
          secret_name = env.value
        }
      }
      liveness_probe {
        transport = "HTTP"
        port      = 8080
        path      = "/healthz"
      }
      readiness_probe {
        transport = "HTTP"
        port      = 8080
        path      = "/readyz"
      }
    }
  }
  depends_on = [azurerm_role_assignment.pull]
}

# --- web --------------------------------------------------------------------------

resource "azurerm_container_app" "web" {
  name                         = "${var.name}-web"
  container_app_environment_id = azurerm_container_app_environment.env.id
  resource_group_name          = azurerm_resource_group.rg.name
  revision_mode                = "Single"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.apps.id]
  }
  registry {
    server   = azurerm_container_registry.acr.login_server
    identity = azurerm_user_assigned_identity.apps.id
  }

  ingress {
    external_enabled = true
    target_port      = 4000
    transport        = "auto"
    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }

  template {
    min_replicas = 1
    max_replicas = 4
    container {
      name   = "web"
      image  = var.web_image
      cpu    = 0.5
      memory = "1Gi"
      dynamic "env" {
        for_each = {
          NODE_ENV             = "production"
          PUBLIC_SITE_URL      = local.site_url
          API_INTERNAL_URL     = "https://${azurerm_container_app.api["api"].ingress[0].fqdn}"
          TRUSTED_PROXY_HOPS   = "1" # Container Apps ingress
          FIREBASE_API_KEY     = var.firebase_api_key
          FIREBASE_AUTH_DOMAIN = var.firebase_auth_domain
          FIREBASE_PROJECT_ID  = var.firebase_project_id
        }
        content {
          name  = env.key
          value = env.value
        }
      }
      liveness_probe {
        transport = "HTTP"
        port      = 4000
        path      = "/healthz"
      }
    }
  }
  depends_on = [azurerm_role_assignment.pull]
}

module "dns" {
  source  = "../modules/cloudflare-dns"
  zone_id = var.cloudflare_zone_id
  domain  = var.domain
  site_records = var.bind_domain ? [
    { name = var.domain, type = "A", content = azurerm_container_app_environment.env.static_ip_address },
    { name = "asuid.${var.domain}", type = "TXT", content = azurerm_container_app.web.custom_domain_verification_id },
    { name = "www.${var.domain}", type = "CNAME", content = azurerm_container_app.web.ingress[0].fqdn },
    { name = "asuid.www.${var.domain}", type = "TXT", content = azurerm_container_app.web.custom_domain_verification_id },
  ] : []
  email_dns_records = var.email_dns_records
  dmarc_policy      = var.dmarc_policy
  dmarc_rua         = var.dmarc_rua
}

# The hostnames are added here; the free managed certificates are issued by
# the Ansible playbook (az containerapp hostname bind), which azurerm cannot do.
resource "azurerm_container_app_custom_domain" "site" {
  for_each         = local.hosts
  name             = each.value
  container_app_id = azurerm_container_app.web.id
  lifecycle {
    ignore_changes = [certificate_binding_type, container_app_environment_certificate_id]
  }
  depends_on = [module.dns] # dns records must exist first
}

output "registry" { value = azurerm_container_registry.acr.login_server }
output "resource_group" { value = azurerm_resource_group.rg.name }
output "environment" { value = azurerm_container_app_environment.env.name }
output "web_app" { value = azurerm_container_app.web.name }
output "web_url" { value = "https://${azurerm_container_app.web.ingress[0].fqdn}" }
output "site_url" { value = local.site_url }
output "hostnames" { value = values(local.hosts) }
output "webhook_urls" {
  value = { brevo = "${local.site_url}/api/webhooks/brevo", mailjet = "${local.site_url}/api/webhooks/mailjet" }
}

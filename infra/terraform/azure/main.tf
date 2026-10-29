# Azure Container Apps deployment: web (public, Angular SSR), api (internal
# ingress, reachable only from inside the environment) and worker (no ingress,
# always one replica). Database: Neon. DNS records live in the dns/ root,
# which the deploy playbook points at this target's site_records.
terraform {
  required_version = ">= 1.9"
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = ">= 4.17, < 6.0" }
  }
  # State holds secret values. Use an encrypted remote backend, for example:
  # backend "azurerm" {
  #   resource_group_name  = "tfstate"
  #   storage_account_name = "cadentictfstate"
  #   container_name       = "tfstate"
  #   key                  = "portfolio-cms-azure.tfstate"
  # }
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
variable "domain_ready" {
  type        = bool
  default     = true
  description = "The domain's DNS records already point here. Azure checks them before it adds a hostname, so the deploy playbook passes false on the first deploy, applies the dns/ root, then applies again with true."
}

provider "azurerm" {
  features {}
  subscription_id = var.azure_subscription_id
  # azurerm 5.x registers no resource providers by default; a new subscription
  # needs these before the first apply.
  resource_providers_to_register = [
    "Microsoft.App",
    "Microsoft.ContainerRegistry",
    "Microsoft.ManagedIdentity",
    "Microsoft.OperationalInsights",
  ]
}

locals {
  site_url = "https://${var.domain}"
  secrets = merge({
    "database-url"                 = var.database_url
    "app-secret"                   = var.app_secret
    "email-brevo-api-key"          = var.email_brevo_api_key
    "email-brevo-webhook-secret"   = var.email_brevo_webhook_secret
    "email-mailjet-api-key"        = var.email_mailjet_api_key
    "email-mailjet-secret-key"     = var.email_mailjet_secret_key
    "email-mailjet-webhook-secret" = var.email_mailjet_webhook_secret
    },
    # A Google bucket is reached with a key limited to it; an Azure container
    # with the API's managed identity (below), so no key.
    var.media_storage == "gcs" ? { "media-gcs-credentials" = var.media_gcs_credentials } : {},
  )
  api_env = merge(local.media_env, {
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
    # Which managed identity the Azure SDK uses (the API's, below).
    AZURE_CLIENT_ID = azurerm_user_assigned_identity.api.client_id
  })
  hosts = var.bind_domain ? { apex = var.domain, www = "www.${var.domain}" } : {}
  # Hostnames are added to the app only once DNS points here (see domain_ready).
  bound_hosts = var.domain_ready ? local.hosts : {}
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
  logs_destination           = "log-analytics" # required with a workspace on azurerm 5.x
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

# The API and worker's own identity. Its only role is on the media container,
# so the web app (which shares the "apps" identity) cannot reach the files.
resource "azurerm_user_assigned_identity" "api" {
  name                = "id-${var.name}-api"
  resource_group_name = azurerm_resource_group.rg.name
  location            = azurerm_resource_group.rg.location
}

# Media container (media-azure/ root, possibly another subscription): read,
# write and delete blobs in that one container, and nothing else.
resource "azurerm_role_assignment" "media" {
  count                = var.media_storage == "azure" ? 1 : 0
  scope                = "/subscriptions/${coalesce(var.media_azure_subscription_id, var.azure_subscription_id)}/resourceGroups/${var.media_azure_resource_group}/providers/Microsoft.Storage/storageAccounts/${var.media_azure_account}/blobServices/default/containers/${var.media_azure_container}"
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azurerm_user_assigned_identity.api.principal_id
  principal_type       = "ServicePrincipal"
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
    identity_ids = [azurerm_user_assigned_identity.apps.id, azurerm_user_assigned_identity.api.id]
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

  lifecycle {
    precondition {
      condition     = var.media_storage != "local"
      error_message = "media_storage = \"local\" would keep uploads on the container's disk, which Container Apps discards. Use a bucket: media_storage = \"azure\" or \"gcs\"."
    }
    precondition {
      condition     = var.media_storage != "gcs" || nonsensitive(var.media_gcs_credentials != "")
      error_message = "media_storage = \"gcs\" on Azure needs a key for the bucket: set media_access_key = true in secret.tfvars and deploy with the playbook, which passes the key in."
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
        for_each = merge(local.api_env, {
          RUN_WORKER = each.value ? "true" : "false"
          # Changing a secret value alone starts no new revision; this does.
          SECRETS_REV = nonsensitive(substr(sha256(jsonencode(local.secrets)), 0, 12))
        })
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
  depends_on = [azurerm_role_assignment.pull, azurerm_role_assignment.media]
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
          NG_ALLOWED_HOSTS     = "${var.domain},www.${var.domain},*.azurecontainerapps.io"
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

# The dns/ root creates these records; this root only describes them. The
# verification id is published in DNS, so it need not stay sensitive (a root
# output may not hold sensitive values).
locals {
  domain_verification_id = nonsensitive(azurerm_container_app.web.custom_domain_verification_id)
  site_records = var.bind_domain ? [
    { name = var.domain, type = "A", content = azurerm_container_app_environment.env.static_ip_address },
    { name = "asuid.${var.domain}", type = "TXT", content = local.domain_verification_id },
    { name = "www.${var.domain}", type = "CNAME", content = azurerm_container_app.web.ingress[0].fqdn },
    { name = "asuid.www.${var.domain}", type = "TXT", content = local.domain_verification_id },
  ] : []
}

# The hostnames are added here; the free managed certificates are issued by
# the Ansible playbook (az containerapp hostname bind), which azurerm cannot do.
resource "azurerm_container_app_custom_domain" "site" {
  for_each         = local.bound_hosts
  name             = each.value
  container_app_id = azurerm_container_app.web.id
  lifecycle {
    ignore_changes = [certificate_binding_type, container_app_environment_certificate_id]
  }
}

output "registry" { value = azurerm_container_registry.acr.login_server }
output "resource_group" { value = azurerm_resource_group.rg.name }
output "environment" { value = azurerm_container_app_environment.env.name }
output "web_app" { value = azurerm_container_app.web.name }
output "web_url" { value = "https://${azurerm_container_app.web.ingress[0].fqdn}" }
output "site_url" { value = local.site_url }
output "hostnames" { value = values(local.bound_hosts) }
# Hostnames that still need adding once DNS points here (first deploy only).
output "pending_hostnames" { value = [for k, h in local.hosts : h if !contains(keys(local.bound_hosts), k)] }
output "webhook_urls" {
  value = { brevo = "${local.site_url}/api/webhooks/brevo", mailjet = "${local.site_url}/api/webhooks/mailjet" }
}
output "site_records" { value = local.site_records }

# Private Azure Blob container for the site's media. It can live in its own
# subscription (media_azure_subscription_id) in the same Entra tenant.
#
# Nothing here is public: anonymous blob access is off, the container is
# private and only HTTPS with TLS 1.2+ is accepted. The site reaches it in one
# of two ways:
#   - Azure Container Apps: the azure/ root gives the API's managed identity
#     Storage Blob Data Contributor on this container only.
#   - Anywhere else (VPS, Cloud Run): with media_access_key = true this root
#     issues a SAS token limited to this container, which the deploy playbook
#     hands to the site. Without it, account keys are switched off entirely.
terraform {
  required_version = ">= 1.9"
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = ">= 4.17, < 6.0" }
    time    = { source = "hashicorp/time", version = ">= 0.11, < 1.0" }
  }
  # State holds the SAS token. Use an encrypted remote backend, for example:
  # backend "azurerm" {
  #   resource_group_name  = "tfstate"
  #   storage_account_name = "cadentictfstate"
  #   container_name       = "tfstate"
  #   key                  = "portfolio-cms-media-azure.tfstate"
  # }
}

variable "azure_subscription_id" {
  type    = string
  default = ""
}

locals {
  subscription_id = coalesce(var.media_azure_subscription_id, var.azure_subscription_id, "unset")
}

provider "azurerm" {
  features {
    # Account keys are off unless media_access_key is set, and the provider
    # signs storage data-plane calls with a key, so it must not make any.
    # Everything here goes through Azure Resource Manager instead.
    storage {
      data_plane_available = false
    }
  }
  subscription_id                = local.subscription_id
  resource_providers_to_register = ["Microsoft.Storage"]
}

data "azurerm_client_config" "me" {}

resource "azurerm_resource_group" "media" {
  name     = var.media_azure_resource_group
  location = var.media_azure_location
}

resource "azurerm_storage_account" "media" {
  name                             = var.media_azure_account
  resource_group_name              = azurerm_resource_group.media.name
  location                         = azurerm_resource_group.media.location
  account_kind                     = "StorageV2"
  account_tier                     = "Standard"
  account_replication_type         = "LRS"
  https_traffic_only_enabled       = true
  min_tls_version                  = "TLS1_2"
  allow_nested_items_to_be_public  = false
  cross_tenant_replication_enabled = false
  default_to_oauth_authentication  = true
  # Account keys only exist to sign the SAS token for a site outside Azure.
  shared_access_key_enabled = var.media_access_key
  blob_properties {
    # Deleted or replaced files stay recoverable for 7 days.
    delete_retention_policy {
      days = 7
    }
    container_delete_retention_policy {
      days = 7
    }
  }
  lifecycle {
    prevent_destroy = true
    precondition {
      condition     = var.media_azure_account != "" && local.subscription_id != "unset"
      error_message = "Set media_azure_account and media_azure_subscription_id (or azure_subscription_id) in secret.tfvars."
    }
  }
}

# Renaming the container would delete it with every file, so Terraform refuses.
resource "azurerm_storage_container" "media" {
  name                  = var.media_azure_container
  storage_account_id    = azurerm_storage_account.media.id
  container_access_type = "private"
  lifecycle {
    prevent_destroy = true
  }
}

# Whoever runs Terraform may read and write the files too, so that
# tools/cloudmigrate can copy media in and out (az --auth-mode login).
resource "azurerm_role_assignment" "deployer" {
  scope                = azurerm_storage_container.media.id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = data.azurerm_client_config.me.object_id
}

# --- SAS token for a site outside Azure --------------------------------------------

# A new token is issued every 6 months (on the next deploy) and each is valid
# for 19, so a site that is deployed at least once a year never sees one expire.
resource "time_rotating" "sas" {
  count           = var.media_access_key ? 1 : 0
  rotation_months = 6
}

data "azurerm_storage_account_blob_container_sas" "site" {
  count             = var.media_access_key ? 1 : 0
  connection_string = azurerm_storage_account.media.primary_connection_string
  container_name    = azurerm_storage_container.media.name
  https_only        = true
  start             = timeadd(time_rotating.sas[0].id, "-1h")    # allow for clock skew
  expiry            = timeadd(time_rotating.sas[0].id, "13896h") # 19 months (579 days)
  permissions {
    read   = true
    add    = true
    create = true
    write  = true
    delete = true
    list   = true
  }
}

output "account" { value = azurerm_storage_account.media.name }
output "container" { value = azurerm_storage_container.media.name }
output "container_id" { value = azurerm_storage_container.media.id }
output "endpoint" { value = azurerm_storage_account.media.primary_blob_endpoint }
# The SAS token (MEDIA_AZURE_SAS, without a leading "?"), or "" without media_access_key.
output "credentials" {
  sensitive = true
  value     = var.media_access_key ? trimprefix(data.azurerm_storage_account_blob_container_sas.site[0].sas, "?") : ""
}
output "sas_expiry" {
  value = var.media_access_key ? data.azurerm_storage_account_blob_container_sas.site[0].expiry : ""
}

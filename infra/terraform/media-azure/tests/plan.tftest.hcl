# Tests for the media-azure root (the private media container on Azure Blob
# storage). The azurerm and time providers are mocked, so nothing reaches
# Azure; every value below is a dummy, never a secret.
#
#   terraform -chdir=infra/terraform/media-azure init -backend=false
#   terraform -chdir=infra/terraform/media-azure test
#
# Most runs are plans. Terraform 1.9 fills computed attributes (a container's
# id, a rotation timestamp) with the mock and override values only on apply.
# The storage account and the container have prevent_destroy, so an apply that
# puts them in state cannot be torn down at the end of the file ("Instance
# cannot be destroyed" during teardown fails the whole file). The one apply
# run targets time_rotating.sas only; it sits near the end, followed only by
# the plan that reads the rotation timestamp it left in state to check the
# SAS validity window.
#
# NOT COVERED, for that reason (an assert on either is "Unknown condition
# value" at plan):
#   - the role assignment's scope. main.tf scopes it to the container
#     (azurerm_storage_container.media.id), but a storage-account-,
#     resource-group- or subscription-wide scope would pass this file too.
#   - the credentials output's value with media_access_key = true (the SAS
#     data source is read only once the account's connection string exists).
#     That it comes from the container SAS, not an account key, can only be
#     confirmed by reading main.tf. Without media_access_key it must be "",
#     and that is checked below.

mock_provider "azurerm" {
  # Whoever runs Terraform: the role assignment's principal comes from here.
  override_data {
    target = data.azurerm_client_config.me
    values = {
      object_id       = "cccccccc-0000-0000-0000-000000000001"
      client_id       = "cccccccc-0000-0000-0000-000000000002"
      tenant_id       = "cccccccc-0000-0000-0000-000000000003"
      subscription_id = "00000000-0000-0000-0000-000000000001"
    }
  }
}

mock_provider "time" {
  # The SAS is issued at a fixed, known time (RFC 3339, as the time provider
  # sets it), so that its start and expiry can be checked exactly.
  override_resource {
    target = time_rotating.sas
    values = {
      id               = "2026-10-28T12:00:00Z"
      rfc3339          = "2026-10-28T12:00:00Z"
      rotation_rfc3339 = "2027-04-28T12:00:00Z"
    }
  }
}

variables {
  database_url                 = "postgresql://dummy:dummy@db.invalid/portfolio?sslmode=require"
  app_secret                   = "test-only-dummy-app-secret-0123456789abcdef"
  admin_emails                 = "admin@site.example"
  firebase_project_id          = "dummy-firebase-project"
  firebase_api_key             = "dummy-firebase-api-key"
  email_brevo_api_key          = "dummy-brevo-api-key"
  email_brevo_webhook_secret   = "dummy-brevo-webhook-secret"
  email_mailjet_api_key        = "dummy-mailjet-api-key"
  email_mailjet_secret_key     = "dummy-mailjet-secret-key"
  email_mailjet_webhook_secret = "dummy-mailjet-webhook-secret"
  cloudflare_api_token         = "dummy-cloudflare-token"
  cloudflare_zone_id           = "dummy-zone-id"

  # Baseline, as the deploy playbook runs this root from secret.tfvars: an
  # Azure container in the site's own subscription and no SAS token. Every
  # run changes only what its scenario needs.
  azure_subscription_id = "00000000-0000-0000-0000-000000000001"
  media_storage         = "azure"
  media_azure_account   = "cadenticmediatest"
}

# --- the storage account -----------------------------------------------------------

# The account is locked down: HTTPS only, TLS 1.2 minimum, no anonymous
# (public) blob access, no cross-tenant replication, Entra ID (OAuth) as the
# portal default, and no shared keys without media_access_key. Deleted blobs
# and containers stay recoverable for 7 days.
run "storage_account_hardening" {
  command = plan

  assert {
    condition     = azurerm_storage_account.media.name == "cadenticmediatest" && output.account == "cadenticmediatest"
    error_message = "The storage account must be named from media_azure_account."
  }

  assert {
    condition     = azurerm_storage_account.media.resource_group_name == "rg-portfolio-media" && azurerm_storage_account.media.location == "centralindia"
    error_message = "The account must go into media_azure_resource_group in media_azure_location (defaults rg-portfolio-media, centralindia)."
  }

  assert {
    condition     = azurerm_storage_account.media.https_traffic_only_enabled == true
    error_message = "The storage account must accept HTTPS only."
  }

  assert {
    condition     = azurerm_storage_account.media.min_tls_version == "TLS1_2"
    error_message = "The storage account must require TLS 1.2 or later."
  }

  assert {
    condition     = azurerm_storage_account.media.allow_nested_items_to_be_public == false
    error_message = "Anonymous (public) blob access must be off on the storage account."
  }

  assert {
    condition     = azurerm_storage_account.media.cross_tenant_replication_enabled == false
    error_message = "Cross-tenant object replication must be off."
  }

  assert {
    condition     = azurerm_storage_account.media.default_to_oauth_authentication == true
    error_message = "The account must default to Entra ID (OAuth) authentication."
  }

  assert {
    condition     = azurerm_storage_account.media.shared_access_key_enabled == false
    error_message = "Shared key (account key) access must be off without media_access_key."
  }

  assert {
    condition     = azurerm_storage_account.media.account_kind == "StorageV2" && azurerm_storage_account.media.account_tier == "Standard" && azurerm_storage_account.media.account_replication_type == "LRS"
    error_message = "The account must be a Standard LRS StorageV2 account."
  }

  assert {
    condition     = azurerm_storage_account.media.blob_properties[0].delete_retention_policy[0].days == 7 && azurerm_storage_account.media.blob_properties[0].container_delete_retention_policy[0].days == 7
    error_message = "Deleted blobs and containers must stay recoverable for 7 days."
  }
}

# media_access_key = true switches shared keys on (they sign the SAS token)
# and changes nothing else: still no public access, HTTPS and TLS 1.2 only,
# and OAuth stays the default.
run "shared_key_only_with_access_key" {
  command = plan

  variables {
    media_access_key = true
  }

  assert {
    condition     = azurerm_storage_account.media.shared_access_key_enabled == true
    error_message = "media_access_key must enable shared key access (needed to sign the SAS)."
  }

  assert {
    condition     = azurerm_storage_account.media.allow_nested_items_to_be_public == false && azurerm_storage_account.media.https_traffic_only_enabled == true && azurerm_storage_account.media.min_tls_version == "TLS1_2"
    error_message = "media_access_key must not relax public access, HTTPS or TLS settings."
  }

  assert {
    condition     = azurerm_storage_account.media.default_to_oauth_authentication == true && azurerm_storage_account.media.cross_tenant_replication_enabled == false
    error_message = "media_access_key must keep OAuth as the default and cross-tenant replication off."
  }
}

# --- the container -----------------------------------------------------------------

# The container is private and named from media_azure_container (default
# "media", which the validation accepts), and the container output says so.
run "private_container_default_name" {
  command = plan

  assert {
    condition     = azurerm_storage_container.media.name == "media" && output.container == "media"
    error_message = "The container must default to \"media\" and the container output must name it."
  }

  assert {
    condition     = azurerm_storage_container.media.container_access_type == "private"
    error_message = "The media container must be private (no anonymous access)."
  }
}

# A name with single hyphens and digits ("site-media-01") is accepted and used
# as is for the container.
run "container_name_with_hyphens_and_digits_accepted" {
  command = plan

  variables {
    media_azure_container = "site-media-01"
  }

  assert {
    condition     = azurerm_storage_container.media.name == "site-media-01" && output.container == "site-media-01"
    error_message = "The container must be named from media_azure_container."
  }

  assert {
    condition     = azurerm_storage_container.media.container_access_type == "private"
    error_message = "The media container must be private whatever its name."
  }
}

# --- the deployer's access ---------------------------------------------------------

# Whoever runs Terraform (the client config's object id) gets Storage Blob
# Data Contributor through exactly this one role assignment, so that
# tools/cloudmigrate can copy files. Its scope is azurerm_storage_container.media.id
# in main.tf; that id is only known after apply, so the scope is NOT checked
# here (see the header).
run "deployer_blob_data_contributor" {
  command = plan

  assert {
    condition     = azurerm_role_assignment.deployer.role_definition_name == "Storage Blob Data Contributor"
    error_message = "The deployer must get Storage Blob Data Contributor (data access only, not Owner or Contributor)."
  }

  assert {
    condition     = azurerm_role_assignment.deployer.principal_id == "cccccccc-0000-0000-0000-000000000001"
    error_message = "The role must go to whoever runs Terraform (azurerm_client_config's object_id)."
  }

  assert {
    condition     = azurerm_role_assignment.deployer.principal_id == data.azurerm_client_config.me.object_id
    error_message = "The role's principal must come from azurerm_client_config."
  }
}

# --- the subscription --------------------------------------------------------------

# Without media_azure_subscription_id, the account goes into the site's own
# subscription (azure_subscription_id), which the azurerm provider uses.
run "site_subscription_by_default" {
  command = plan

  assert {
    condition     = local.subscription_id == "00000000-0000-0000-0000-000000000001"
    error_message = "Without media_azure_subscription_id the provider must use azure_subscription_id."
  }
}

# media_azure_subscription_id wins over azure_subscription_id (a container in
# another subscription of the same tenant).
run "media_subscription_overrides_site_subscription" {
  command = plan

  variables {
    media_azure_subscription_id = "00000000-0000-0000-0000-000000000002"
  }

  assert {
    condition     = local.subscription_id == "00000000-0000-0000-0000-000000000002"
    error_message = "media_azure_subscription_id must take precedence over azure_subscription_id."
  }
}

# A site outside Azure (VPS, Cloud Run) may have no azure_subscription_id at
# all: media_azure_subscription_id alone satisfies the precondition (this run
# expects no failure, so the plan itself is the check).
run "media_subscription_without_site_subscription" {
  command = plan

  variables {
    azure_subscription_id       = ""
    media_azure_subscription_id = "00000000-0000-0000-0000-000000000002"
  }

  assert {
    condition     = local.subscription_id == "00000000-0000-0000-0000-000000000002"
    error_message = "media_azure_subscription_id alone must select the subscription."
  }
}

# --- refused configurations: preconditions -----------------------------------------

# No account name (media_storage left at local, so the variable validation does
# not catch it first): the storage account's precondition stops the plan.
run "missing_account_name_refused" {
  command = plan

  variables {
    media_storage       = "local"
    media_azure_account = ""
  }

  expect_failures = [
    azurerm_storage_account.media,
  ]
}

# Neither media_azure_subscription_id nor azure_subscription_id: the storage
# account's precondition stops the plan instead of using a subscription
# called "unset".
run "missing_subscription_refused" {
  command = plan

  variables {
    azure_subscription_id       = ""
    media_azure_subscription_id = ""
  }

  expect_failures = [
    azurerm_storage_account.media,
  ]
}

# --- refused configurations: variable validations ----------------------------------

# media_storage = "azure" without an account name is rejected by media_storage's
# validation. Terraform 1.9 keeps planning after a failed validation, so the
# storage account's precondition fails as well.
run "azure_without_account_rejected" {
  command = plan

  variables {
    media_azure_account = ""
  }

  expect_failures = [
    var.media_storage,
    azurerm_storage_account.media,
  ]
}

# media_storage accepts only local, gcs or azure (lower case).
run "media_storage_unknown_value_rejected" {
  command = plan

  variables {
    media_storage = "Azure"
  }

  expect_failures = [
    var.media_storage,
  ]
}

# Storage account names are lowercase letters and digits only: a hyphenated
# name is rejected.
run "account_name_with_hyphen_rejected" {
  command = plan

  variables {
    media_azure_account = "cadentic-media"
  }

  expect_failures = [
    var.media_azure_account,
  ]
}

# Storage account names are at most 24 characters (25 here).
run "account_name_too_long_rejected" {
  command = plan

  variables {
    media_azure_account = "cadenticportfoliomedia123"
  }

  expect_failures = [
    var.media_azure_account,
  ]
}

# Container names are lower case only: "Media" is rejected.
run "container_name_uppercase_rejected" {
  command = plan

  variables {
    media_azure_container = "Media"
  }

  expect_failures = [
    var.media_azure_container,
  ]
}

# Container names are at least 3 characters: "a" is rejected.
run "container_name_too_short_rejected" {
  command = plan

  variables {
    media_azure_container = "a"
  }

  expect_failures = [
    var.media_azure_container,
  ]
}

# Hyphens must be single: "my--media" is rejected.
run "container_name_double_hyphen_rejected" {
  command = plan

  variables {
    media_azure_container = "my--media"
  }

  expect_failures = [
    var.media_azure_container,
  ]
}

# Names start with a letter or digit: "-media" is rejected.
run "container_name_leading_hyphen_rejected" {
  command = plan

  variables {
    media_azure_container = "-media"
  }

  expect_failures = [
    var.media_azure_container,
  ]
}

# Names end with a letter or digit: "media-" is rejected.
run "container_name_trailing_hyphen_rejected" {
  command = plan

  variables {
    media_azure_container = "media-"
  }

  expect_failures = [
    var.media_azure_container,
  ]
}

# 63 characters is the longest name Azure allows, and it is accepted.
run "container_name_63_characters_accepted" {
  command = plan

  variables {
    media_azure_container = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0"
  }

  assert {
    condition     = azurerm_storage_container.media.name == "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0"
    error_message = "A 63-character container name must be accepted and used as is."
  }
}

# 64 characters without hyphens is rejected by the variable validation.
run "container_name_64_characters_rejected" {
  command = plan

  variables {
    media_azure_container = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz01"
  }

  expect_failures = [
    var.media_azure_container,
  ]
}

# Names are at most 63 characters, hyphens included.
run "container_name_too_long_rejected" {
  command = plan

  variables {
    media_azure_container = "a-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b-b"
  }

  expect_failures = [
    var.media_azure_container,
  ]
}

# "a-b" is a valid 3-character name.
run "container_name_three_characters_with_hyphen_accepted" {
  command = plan

  variables {
    media_azure_container = "a-b"
  }

  assert {
    condition     = azurerm_storage_container.media.name == "a-b"
    error_message = "\"a-b\" is a valid container name and must be accepted."
  }
}

# --- the SAS token -----------------------------------------------------------------

# media_access_key = false (the default, for a site on Azure Container Apps
# using its managed identity): no rotation, no SAS, and the credentials and
# sas_expiry outputs the deploy playbook reads are empty.
run "no_sas_without_access_key" {
  command = plan

  assert {
    condition     = length(time_rotating.sas) == 0
    error_message = "No SAS rotation may be created without media_access_key."
  }

  assert {
    condition     = length(data.azurerm_storage_account_blob_container_sas.site) == 0
    error_message = "No SAS may be issued without media_access_key."
  }

  assert {
    condition     = output.credentials == ""
    error_message = "The credentials output must be empty without media_access_key."
  }

  assert {
    condition     = output.sas_expiry == ""
    error_message = "The sas_expiry output must be empty without media_access_key."
  }
}

# media_access_key = true: one SAS, signed for this container (a blob
# container SAS, not an account SAS), HTTPS only, with object read/write/list
# permissions and none of the ownership, ACL or immutability ones. One
# time_rotating resource renews it every 6 months.
run "sas_scoped_to_container" {
  command = plan

  variables {
    media_access_key = true
  }

  assert {
    condition     = length(data.azurerm_storage_account_blob_container_sas.site) == 1
    error_message = "media_access_key must issue exactly one SAS."
  }

  assert {
    condition     = data.azurerm_storage_account_blob_container_sas.site[0].container_name == "media" && data.azurerm_storage_account_blob_container_sas.site[0].container_name == azurerm_storage_container.media.name
    error_message = "The SAS must be limited to the media container."
  }

  assert {
    condition     = data.azurerm_storage_account_blob_container_sas.site[0].https_only == true
    error_message = "The SAS must be valid over HTTPS only."
  }

  assert {
    condition = alltrue([
      for p in ["read", "add", "create", "write", "delete", "list"] :
      data.azurerm_storage_account_blob_container_sas.site[0].permissions[0][p] == true
    ])
    error_message = "The SAS must allow read, add, create, write, delete and list on blobs."
  }

  assert {
    condition = alltrue([
      for p in ["ownership", "permissions", "set_immutability_policy", "execute", "move", "delete_version", "tags", "find"] :
      !coalesce(data.azurerm_storage_account_blob_container_sas.site[0].permissions[0][p], false)
    ])
    error_message = "The SAS must not grant ownership, ACL, immutability, version-delete, tag or other extra permissions."
  }

  assert {
    condition     = length(time_rotating.sas) == 1 && time_rotating.sas[0].rotation_months == 6
    error_message = "One time_rotating resource must renew the SAS every 6 months."
  }

  assert {
    condition     = time_rotating.sas[0].rotation_years == null && time_rotating.sas[0].rotation_days == null && time_rotating.sas[0].rotation_hours == null && time_rotating.sas[0].rotation_minutes == null
    error_message = "rotation_months alone must set the renewal period."
  }
}

# Issue the SAS rotation (against the mock): only time_rotating.sas reaches
# state, so the teardown never has to destroy the protected account or
# container. Its timestamp (2026-10-28T12:00:00Z, from the override above)
# lets the next run compute the SAS window at plan time.
run "sas_rotation_issued" {
  command = apply

  variables {
    media_access_key = true
  }

  plan_options {
    target = [time_rotating.sas]
  }

  assert {
    condition     = length(time_rotating.sas) == 1 && time_rotating.sas[0].rotation_months == 6
    error_message = "The SAS rotation must be created with a 6-month period."
  }
}

# With the rotation timestamp in state, the SAS starts 1 hour before it was
# issued (clock skew) and expires 13896 hours (19 months) after, so it
# outlives the next 6-month rotation by a year. sas_expiry reports that date.
run "sas_validity_window" {
  command = plan

  variables {
    media_access_key = true
  }

  assert {
    condition     = data.azurerm_storage_account_blob_container_sas.site[0].start == "2026-10-28T11:00:00Z"
    error_message = "The SAS must start 1 hour before it is issued, to allow for clock skew."
  }

  assert {
    condition     = timecmp(data.azurerm_storage_account_blob_container_sas.site[0].start, time_rotating.sas[0].id) < 0 && timeadd(data.azurerm_storage_account_blob_container_sas.site[0].start, "1h") == time_rotating.sas[0].id
    error_message = "The SAS start must be exactly 1 hour before the rotation timestamp."
  }

  assert {
    condition     = data.azurerm_storage_account_blob_container_sas.site[0].expiry == "2028-05-29T12:00:00Z"
    error_message = "The SAS must expire 19 months (13896 hours) after it is issued."
  }

  assert {
    condition = (
      timecmp(data.azurerm_storage_account_blob_container_sas.site[0].expiry, timeadd(time_rotating.sas[0].id, "13872h")) >= 0 &&
      timecmp(data.azurerm_storage_account_blob_container_sas.site[0].expiry, timeadd(time_rotating.sas[0].id, "13944h")) <= 0
    )
    error_message = "The SAS lifetime must be about 19 months (578 to 581 days)."
  }

  # Rotation period (an average month is 730 h) plus a year (8760 h).
  assert {
    condition     = timecmp(data.azurerm_storage_account_blob_container_sas.site[0].expiry, timeadd(time_rotating.sas[0].id, "${time_rotating.sas[0].rotation_months * 730 + 8760}h")) >= 0
    error_message = "The SAS must stay valid for a year after the next rotation is due."
  }

  assert {
    condition     = output.sas_expiry == "2028-05-29T12:00:00Z"
    error_message = "The sas_expiry output must report the SAS expiry."
  }
}

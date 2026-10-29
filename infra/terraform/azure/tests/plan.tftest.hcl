# Plan tests for the Azure Container Apps root. Nothing here reaches Azure:
# the azurerm provider is mocked. Most runs only plan; the two that need
# computed values (identity ids, the domain verification id) apply against
# the mock, with well-formed ARM ids below so the provider's own validation
# accepts them.
#
#   terraform -chdir=infra/terraform/azure init -backend=false
#   terraform -chdir=infra/terraform/azure test

mock_provider "azurerm" {
  mock_resource "azurerm_log_analytics_workspace" {
    defaults = {
      id = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-portfolio/providers/Microsoft.OperationalInsights/workspaces/log-portfolio"
    }
  }
  mock_resource "azurerm_container_app_environment" {
    defaults = {
      id                = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-portfolio/providers/Microsoft.App/managedEnvironments/cae-portfolio"
      static_ip_address = "20.0.0.10"
    }
  }
  mock_resource "azurerm_container_registry" {
    defaults = {
      id           = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-portfolio/providers/Microsoft.ContainerRegistry/registries/cadenticportfolio"
      login_server = "cadenticportfolio.azurecr.io"
    }
  }
  mock_resource "azurerm_user_assigned_identity" {
    defaults = {
      id           = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-portfolio/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id-portfolio"
      principal_id = "aaaaaaaa-0000-0000-0000-000000000001"
      client_id    = "aaaaaaaa-0000-0000-0000-000000000002"
      tenant_id    = "aaaaaaaa-0000-0000-0000-000000000003"
    }
  }
  mock_resource "azurerm_container_app" {
    defaults = {
      id = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-portfolio/providers/Microsoft.App/containerApps/portfolio"
    }
  }
}

# The API's identity must look different from the shared "apps" identity, so
# that a test can tell which one got the media role and AZURE_CLIENT_ID.
override_resource {
  target = azurerm_user_assigned_identity.api
  values = {
    id           = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-portfolio/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id-portfolio-api"
    principal_id = "bbbbbbbb-0000-0000-0000-000000000001"
    client_id    = "bbbbbbbb-0000-0000-0000-000000000002"
  }
}

# Fixed values for what site_records publishes from the web app.
override_resource {
  target = azurerm_container_app.web
  values = {
    id                            = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-portfolio/providers/Microsoft.App/containerApps/portfolio-web"
    custom_domain_verification_id = "MOCK-VERIFICATION-ID"
    ingress                       = { fqdn = "portfolio-web.mock-env.centralindia.azurecontainerapps.io" }
  }
}

# Dummy values only; none of these is a real secret.
variables {
  azure_subscription_id        = "00000000-0000-0000-0000-000000000001"
  api_image                    = "cadenticportfolio.azurecr.io/api:test"
  web_image                    = "cadenticportfolio.azurecr.io/web:test"
  database_url                 = "postgresql://dummy:dummy@db.example.invalid/portfolio?sslmode=require"
  app_secret                   = "dummy-app-secret-for-tests-0123456789abcdef"
  admin_emails                 = "admin@example.invalid"
  firebase_project_id          = "dummy-firebase-project"
  firebase_api_key             = "dummy-firebase-api-key"
  email_brevo_api_key          = "dummy-brevo-api-key"
  email_brevo_webhook_secret   = "dummy-brevo-webhook-secret"
  email_mailjet_api_key        = "dummy-mailjet-api-key"
  email_mailjet_secret_key     = "dummy-mailjet-secret-key"
  email_mailjet_webhook_secret = "dummy-mailjet-webhook-secret"
  cloudflare_api_token         = "dummy-cloudflare-token"
  cloudflare_zone_id           = "dummy-zone-id"
  media_storage                = "azure"
  media_azure_account          = "cadenticmedia"
}

# --- media in an Azure container (plan) -----------------------------------------

# Same subscription: the media role is Storage Blob Data Contributor on the one
# container (not the account) in the site's subscription, the API and worker
# are told which identity to use, and no key (SAS or Google) is handed out.
run "azure_media_same_subscription" {
  command = plan

  assert {
    condition     = length(azurerm_role_assignment.media) == 1
    error_message = "media_storage = \"azure\" should create exactly one media role assignment."
  }
  assert {
    condition     = azurerm_role_assignment.media[0].scope == "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-portfolio-media/providers/Microsoft.Storage/storageAccounts/cadenticmedia/blobServices/default/containers/media"
    error_message = "The media role must be scoped to the media container in the site's subscription, got ${azurerm_role_assignment.media[0].scope}."
  }
  assert {
    condition     = azurerm_role_assignment.media[0].role_definition_name == "Storage Blob Data Contributor" && azurerm_role_assignment.media[0].principal_type == "ServicePrincipal"
    error_message = "The media role must be Storage Blob Data Contributor for a service principal (the managed identity)."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      nonsensitive(toset([for s in app.secret : s.name])) == toset([
        "database-url", "app-secret", "email-brevo-api-key", "email-brevo-webhook-secret",
        "email-mailjet-api-key", "email-mailjet-secret-key", "email-mailjet-webhook-secret",
      ])
    ])
    error_message = "With an Azure container the API and worker must get only the app secrets: no SAS token and no Google key."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      { for e in app.template[0].container[0].env : e.name => e.value if startswith(e.name, "MEDIA_") } == {
        MEDIA_STORAGE         = "azure"
        MEDIA_AZURE_ACCOUNT   = "cadenticmedia"
        MEDIA_AZURE_CONTAINER = "media"
      }
    ])
    error_message = "The API and worker must get MEDIA_STORAGE, MEDIA_AZURE_ACCOUNT and MEDIA_AZURE_CONTAINER, and no MEDIA_AZURE_SAS or MEDIA_GCS_* setting."
  }
  assert {
    condition     = alltrue([for app in azurerm_container_app.api : contains([for e in app.template[0].container[0].env : e.name], "AZURE_CLIENT_ID")])
    error_message = "The API and worker must get AZURE_CLIENT_ID, so the Azure SDK picks the API identity."
  }
}

# Another subscription: the role is granted on the container in the media
# subscription and resource group, not in the site's subscription.
run "azure_media_other_subscription" {
  command = plan

  variables {
    media_azure_subscription_id = "00000000-0000-0000-0000-000000000002"
    media_azure_resource_group  = "rg-shared-media"
    media_azure_account         = "sharedmedia"
    media_azure_container       = "site-media"
  }

  assert {
    condition     = azurerm_role_assignment.media[0].scope == "/subscriptions/00000000-0000-0000-0000-000000000002/resourceGroups/rg-shared-media/providers/Microsoft.Storage/storageAccounts/sharedmedia/blobServices/default/containers/site-media"
    error_message = "The media role must be scoped to the container in media_azure_subscription_id, got ${azurerm_role_assignment.media[0].scope}."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      { for e in app.template[0].container[0].env : e.name => e.value if startswith(e.name, "MEDIA_AZURE_") } == {
        MEDIA_AZURE_ACCOUNT   = "sharedmedia"
        MEDIA_AZURE_CONTAINER = "site-media"
      }
    ])
    error_message = "The API and worker must point at the other subscription's account and container, with no SAS token."
  }
}

# Even when the playbook hands in a SAS token (media_access_key left on while a
# move between clouds finishes, so the media root still issues one), the Azure
# site keeps using its managed identity: the token becomes no secret and no
# environment variable. (The run above has no token to leak, so it cannot
# show this.)
run "azure_media_ignores_sas_token" {
  command = plan

  variables {
    media_access_key = true
    media_azure_sas  = "sv=2022-11-02&sr=c&sp=racwdl&sig=DUMMY-NOT-A-REAL-SIGNATURE"
  }

  assert {
    condition     = length(azurerm_role_assignment.media) == 1
    error_message = "With a SAS token on offer the API must still reach the container through its identity's role."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      nonsensitive(toset([for s in app.secret : s.name])) == toset([
        "database-url", "app-secret", "email-brevo-api-key", "email-brevo-webhook-secret",
        "email-mailjet-api-key", "email-mailjet-secret-key", "email-mailjet-webhook-secret",
      ]) && alltrue([for s in app.secret : s.value != var.media_azure_sas])
    ])
    error_message = "The SAS token must not become a secret of the API or worker."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      !contains([for e in app.template[0].container[0].env : e.name], "MEDIA_AZURE_SAS")
    ])
    error_message = "The API and worker must not get MEDIA_AZURE_SAS on Azure; they use the managed identity."
  }
}

# --- exposure (plan) ------------------------------------------------------------

# Only the web app is reachable from the internet: the API has internal ingress
# (other apps in the environment only), the worker has none, and the registry
# has no admin user (no shared password; pulls use the managed identity).
run "api_and_worker_not_public" {
  command = plan

  assert {
    condition     = length(azurerm_container_app.api["api"].ingress) == 1 && azurerm_container_app.api["api"].ingress[0].external_enabled == false
    error_message = "The API's ingress must be internal (external_enabled = false)."
  }
  assert {
    condition     = length(azurerm_container_app.api["worker"].ingress) == 0
    error_message = "The worker must have no ingress at all."
  }
  assert {
    condition     = azurerm_container_registry.acr.admin_enabled == false
    error_message = "The registry admin user must stay disabled."
  }
}

# --- media in a Google bucket (plan) -------------------------------------------

# A Google bucket (here in another Google Cloud project) is reached with the
# key the media-gcs root issues: it becomes the media-gcs-credentials secret,
# read as MEDIA_GCS_CREDENTIALS, and no Azure role is granted.
run "gcs_media_with_access_key" {
  command = plan

  variables {
    media_storage         = "gcs"
    media_gcs_bucket      = "cadentic-portfolio-media"
    media_gcs_project_id  = "cadentic-media-project"
    media_access_key      = true
    media_gcs_credentials = "{\"type\":\"service_account\",\"project_id\":\"cadentic-media-project\",\"private_key\":\"dummy-not-a-key\"}"
  }

  assert {
    condition     = length(azurerm_role_assignment.media) == 0
    error_message = "A Google bucket must not create an Azure media role assignment."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      [for s in app.secret : s.value if s.name == "media-gcs-credentials"] == [var.media_gcs_credentials]
    ])
    error_message = "The API and worker must hold the bucket key as the media-gcs-credentials secret."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      [for e in app.template[0].container[0].env : e.secret_name if e.name == "MEDIA_GCS_CREDENTIALS"] == ["media-gcs-credentials"]
    ])
    error_message = "MEDIA_GCS_CREDENTIALS must come from the media-gcs-credentials secret on both the API and the worker."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      { for e in app.template[0].container[0].env : e.name => e.value if startswith(e.name, "MEDIA_") && e.secret_name == null } == {
        MEDIA_STORAGE    = "gcs"
        MEDIA_GCS_BUCKET = "cadentic-portfolio-media"
      }
    ])
    error_message = "The API and worker must get MEDIA_STORAGE = gcs and MEDIA_GCS_BUCKET, and no MEDIA_AZURE_* setting."
  }
  assert {
    condition     = length(azurerm_container_app.web.secret) == 0 && !contains([for e in azurerm_container_app.web.template[0].container[0].env : e.name], "MEDIA_GCS_CREDENTIALS")
    error_message = "The bucket key must reach only the API and worker, never the public web app."
  }
}

# --- preconditions --------------------------------------------------------------

# media_storage = "local" would keep uploads on a disk Container Apps discards;
# the precondition on the API/worker apps refuses it.
run "local_media_refused" {
  command = plan

  variables {
    media_storage = "local"
  }

  expect_failures = [azurerm_container_app.api]
}

# A Google bucket without a key (media_access_key off, or not deployed through
# the playbook) is refused by the gcs-credentials precondition.
run "gcs_media_without_key_refused" {
  command = plan

  variables {
    media_storage    = "gcs"
    media_gcs_bucket = "cadentic-portfolio-media"
    media_access_key = false
  }

  expect_failures = [azurerm_container_app.api]
}

# --- variable validations -------------------------------------------------------

# media_storage accepts only local, gcs or azure.
run "unknown_media_storage_rejected" {
  command = plan

  variables {
    media_storage = "s3"
  }

  expect_failures = [var.media_storage]
}

# media_storage = "azure" needs the storage account name.
run "azure_media_needs_account" {
  command = plan

  variables {
    media_azure_account = ""
  }

  expect_failures = [var.media_storage]
}

# media_storage = "gcs" needs the bucket name.
run "gcs_media_needs_bucket" {
  command = plan

  variables {
    media_storage         = "gcs"
    media_gcs_credentials = "{\"type\":\"service_account\"}"
  }

  expect_failures = [var.media_storage]
}

# A storage account name is 3-24 lowercase letters and digits.
run "bad_azure_account_name_rejected" {
  command = plan

  variables {
    media_azure_account = "Cadentic-Media"
  }

  expect_failures = [var.media_azure_account]
}

# A container name is lowercase letters, digits and single hyphens.
run "bad_azure_container_name_rejected" {
  command = plan

  variables {
    media_azure_container = "site--media"
  }

  expect_failures = [var.media_azure_container]
}

# A bucket name has no dots or capitals.
run "bad_gcs_bucket_name_rejected" {
  command = plan

  variables {
    media_storage         = "gcs"
    media_gcs_bucket      = "Cadentic.Media"
    media_gcs_credentials = "{\"type\":\"service_account\"}"
  }

  expect_failures = [var.media_gcs_bucket]
}

# --- hostnames on the web app ------------------------------------------------------

# The apex and www keep the keys apex and www, so states made before site_host
# existed do not remove and re-add them.
run "apex_hostname_keys" {
  command = plan

  assert {
    condition     = { for k, d in azurerm_container_app_custom_domain.site : k => d.name } == { apex = "cadentic.dev", www = "www.cadentic.dev" }
    error_message = "The apex and www custom domains must keep the keys apex and www."
  }
}

# domain_ready = false keeps only the hostnames already on the app
# (ready_hosts); a new site_host waits for DNS and is listed as pending.
run "new_site_host_waits_for_dns" {
  command = plan

  variables {
    site_host    = "profile.cadentic.dev"
    site_www     = true
    domain_ready = false
    ready_hosts  = ["www.profile.cadentic.dev", "cadentic.dev"]
  }

  assert {
    condition     = keys(azurerm_container_app_custom_domain.site) == ["www.profile.cadentic.dev"]
    error_message = "Only a hostname that is both served and already on the app may stay before DNS points here."
  }
  assert {
    condition     = output.pending_hostnames == ["profile.cadentic.dev"]
    error_message = "The new subdomain must be pending until DNS points here."
  }
}

# --- computed values (apply against the mock) -----------------------------------
# Identity ids and the verification id are unknown in a plan, so these runs
# apply against the mocked provider. They come last so the plan runs above
# start from an empty state.

# The media role goes to the API's own identity (not the shared "apps" one),
# AZURE_CLIENT_ID is that identity's client id, the API and worker both carry
# it, and the web app does not.
run "azure_media_api_identity" {
  command = apply

  assert {
    condition     = azurerm_role_assignment.media[0].principal_id == azurerm_user_assigned_identity.api.principal_id && azurerm_role_assignment.media[0].principal_id == "bbbbbbbb-0000-0000-0000-000000000001"
    error_message = "The media role must be granted to the API identity, got principal ${azurerm_role_assignment.media[0].principal_id}."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      [for e in app.template[0].container[0].env : e.value if e.name == "AZURE_CLIENT_ID"] == ["bbbbbbbb-0000-0000-0000-000000000002"]
    ])
    error_message = "AZURE_CLIENT_ID on the API and worker must be the API identity's client id."
  }
  assert {
    condition = alltrue([for app in azurerm_container_app.api :
      app.identity[0].type == "UserAssigned" && contains(app.identity[0].identity_ids, azurerm_user_assigned_identity.api.id)
    ])
    error_message = "The API and worker must both carry the API identity."
  }
  assert {
    condition     = !contains(azurerm_container_app.web.identity[0].identity_ids, azurerm_user_assigned_identity.api.id)
    error_message = "The web app must not carry the API identity, or it could reach the media container."
  }
  # The only other role: the shared "apps" identity (which the web app also
  # carries) may pull images from the one registry, and nothing wider.
  assert {
    condition     = azurerm_role_assignment.pull.role_definition_name == "AcrPull" && azurerm_role_assignment.pull.scope == azurerm_container_registry.acr.id && azurerm_role_assignment.pull.principal_id == azurerm_user_assigned_identity.apps.principal_id
    error_message = "The apps identity must only get AcrPull on the registry (not the resource group or subscription), got ${azurerm_role_assignment.pull.role_definition_name} on ${azurerm_role_assignment.pull.scope}."
  }
}

# site_records (read by the dns/ root) holds the apex A record, the www CNAME
# and both asuid TXT records carrying the custom domain verification id.
run "site_records_for_dns_root" {
  command = apply

  assert {
    condition     = length(output.site_records) == 4
    error_message = "site_records should hold exactly 4 records, got ${length(output.site_records)}."
  }
  assert {
    condition = { for r in output.site_records : "${r.type} ${r.name}" => r.content } == {
      "A cadentic.dev"             = "20.0.0.10"
      "CNAME www.cadentic.dev"     = "portfolio-web.mock-env.centralindia.azurecontainerapps.io"
      "TXT asuid.cadentic.dev"     = "MOCK-VERIFICATION-ID"
      "TXT asuid.www.cadentic.dev" = "MOCK-VERIFICATION-ID"
    }
    error_message = "site_records is wrong: ${jsonencode(output.site_records)}"
  }
  assert {
    condition     = alltrue([for r in output.site_records : r.content == nonsensitive(azurerm_container_app.web.custom_domain_verification_id) if r.type == "TXT"])
    error_message = "Both asuid TXT records must hold the web app's custom_domain_verification_id."
  }
}

# site_host on a subdomain: a CNAME to the web app and its asuid TXT record,
# nothing on the apex (where the mail records stay).
run "subdomain_site_records" {
  command = apply

  variables {
    site_host = "profile.cadentic.dev"
  }

  assert {
    condition = { for r in output.site_records : "${r.type} ${r.name}" => r.content } == {
      "CNAME profile.cadentic.dev"     = "portfolio-web.mock-env.centralindia.azurecontainerapps.io"
      "TXT asuid.profile.cadentic.dev" = "MOCK-VERIFICATION-ID"
    }
    error_message = "site_records is wrong: ${jsonencode(output.site_records)}"
  }
  assert {
    condition     = output.site_url == "https://profile.cadentic.dev" && output.zone_apex == "cadentic.dev"
    error_message = "site_url must be the subdomain and zone_apex the domain."
  }
  assert {
    condition     = jsonencode(output.hostnames) == jsonencode(["profile.cadentic.dev"]) && jsonencode(keys(azurerm_container_app_custom_domain.site)) == jsonencode(["profile.cadentic.dev"])
    error_message = "Only the subdomain must be added to the web app, keyed by its name."
  }
}

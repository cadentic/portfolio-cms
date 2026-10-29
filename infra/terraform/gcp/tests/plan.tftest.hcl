# Tests for the Cloud Run root. The google provider is mocked, so nothing
# reaches Google Cloud; every value below is a dummy, never a secret.
#
#   terraform -chdir=infra/terraform/gcp init -backend=false
#   terraform -chdir=infra/terraform/gcp test
#
# Most runs are plans. The few that compare service account identities or
# service URLs use command = apply, because Terraform 1.9 fills computed
# attributes with the mock and override values only on apply (override_during
# came later). Against the mock provider that apply creates nothing; those runs
# sit at the end so no plan run depends on the state they leave.

mock_provider "google" {
  override_resource {
    target = google_service_account.api
    values = {
      email  = "portfolio-api@site-project-test.iam.gserviceaccount.com"
      member = "serviceAccount:portfolio-api@site-project-test.iam.gserviceaccount.com"
    }
  }

  override_resource {
    target = google_service_account.web
    values = {
      email  = "portfolio-web@site-project-test.iam.gserviceaccount.com"
      member = "serviceAccount:portfolio-web@site-project-test.iam.gserviceaccount.com"
    }
  }

  override_resource {
    target = google_cloud_run_v2_service.api
    values = {
      uri = "https://portfolio-api-test-as.a.run.app"
    }
  }
}

variables {
  gcp_project_id = "site-project-test"
  domain         = "site.example"
  api_image      = "asia-southeast1-docker.pkg.dev/site-project-test/portfolio/api:test"
  web_image      = "asia-southeast1-docker.pkg.dev/site-project-test/portfolio/web:test"

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

  # Baseline: a Google bucket in the site's own project. The default
  # media_storage ("local") is refused on Cloud Run, so every run starts from
  # this and changes only what its scenario needs.
  media_storage    = "gcs"
  media_gcs_bucket = "portfolio-media-test"
}

# --- media in a Google bucket ------------------------------------------------------

# A bucket in the same project: api and worker get MEDIA_STORAGE=gcs and the
# bucket name, one roles/storage.objectUser grant is made on that bucket (the
# root declares no project-level IAM resource at all), and no key is stored:
# only the seven application secrets go to Secret Manager.
run "gcs_same_project" {
  command = plan

  assert {
    condition     = { for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name => e.value if e.value != null }["MEDIA_STORAGE"] == "gcs"
    error_message = "The api service must get MEDIA_STORAGE=gcs."
  }

  assert {
    condition     = { for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name => e.value if e.value != null }["MEDIA_GCS_BUCKET"] == "portfolio-media-test"
    error_message = "The api service must get MEDIA_GCS_BUCKET set to media_gcs_bucket."
  }

  assert {
    condition     = length([for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name if startswith(e.name, "MEDIA_AZURE_")]) == 0
    error_message = "A gcs site must not get any MEDIA_AZURE_* variable."
  }

  assert {
    condition     = { for e in google_cloud_run_v2_service.api["worker"].template[0].containers[0].env : e.name => e.value if e.value != null }["MEDIA_GCS_BUCKET"] == "portfolio-media-test"
    error_message = "The worker shares the api configuration and must get the same MEDIA_GCS_BUCKET."
  }

  assert {
    condition     = length(google_storage_bucket_iam_member.media) == 1
    error_message = "Exactly one bucket IAM member is expected for media_storage = gcs."
  }

  assert {
    condition     = google_storage_bucket_iam_member.media[0].bucket == "portfolio-media-test"
    error_message = "The storage grant must be scoped to the media bucket, not the project."
  }

  assert {
    condition     = google_storage_bucket_iam_member.media[0].role == "roles/storage.objectUser"
    error_message = "The API needs object access only (roles/storage.objectUser), not bucket or project admin."
  }

  assert {
    condition = toset(keys(google_secret_manager_secret.s)) == toset([
      "DATABASE_URL", "APP_SECRET", "EMAIL_BREVO_API_KEY", "EMAIL_BREVO_WEBHOOK_SECRET",
      "EMAIL_MAILJET_API_KEY", "EMAIL_MAILJET_SECRET_KEY", "EMAIL_MAILJET_WEBHOOK_SECRET",
    ])
    error_message = "A gcs site stores only the seven application secrets: no MEDIA_AZURE_SAS and no bucket key."
  }
}

# media_access_key = true and keys handed in (as during a move between clouds:
# the bucket's service-account key, and a SAS token left over from the Azure
# container) change nothing on Cloud Run with a Google bucket: the API keeps
# using its own identity, so neither key is stored nor passed to the services.
run "gcs_key_not_used_on_cloud_run" {
  command = plan

  variables {
    media_access_key      = true
    media_gcs_credentials = "{\"type\":\"service_account\",\"private_key\":\"dummy-not-a-key\"}"
    media_azure_sas       = "sv=2099-01-01&sr=c&sp=racwdl&sig=dummy-leftover-not-a-signature"
  }

  assert {
    condition = toset(keys(google_secret_manager_secret.s)) == toset([
      "DATABASE_URL", "APP_SECRET", "EMAIL_BREVO_API_KEY", "EMAIL_BREVO_WEBHOOK_SECRET",
      "EMAIL_MAILJET_API_KEY", "EMAIL_MAILJET_SECRET_KEY", "EMAIL_MAILJET_WEBHOOK_SECRET",
    ])
    error_message = "Neither the bucket key nor a leftover SAS token may be stored in Secret Manager for a gcs site on Cloud Run."
  }

  assert {
    condition     = length([for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name if strcontains(e.name, "CREDENTIALS") || startswith(e.name, "MEDIA_AZURE_")]) == 0
    error_message = "The api service must not get a MEDIA_GCS_CREDENTIALS or MEDIA_AZURE_* variable."
  }

  assert {
    condition = nonsensitive(alltrue([for svc in google_cloud_run_v2_service.api : alltrue([
      for e in svc.template[0].containers[0].env : e.value != var.media_gcs_credentials && e.value != var.media_azure_sas
    ])]))
    error_message = "Neither key may reach the api or worker as a plain environment value, under any name."
  }

  assert {
    condition     = length(google_storage_bucket_iam_member.media) == 1 && google_storage_bucket_iam_member.media[0].bucket == "portfolio-media-test"
    error_message = "With a key present the bucket grant must still be made."
  }
}

# A bucket in another Google Cloud project (media_gcs_project_id): the grant
# names the bucket, which is global, so it reaches across projects, while the
# site's own resources stay in gcp_project_id.
run "gcs_other_project" {
  command = plan

  variables {
    media_gcs_project_id = "media-project-test"
    media_gcs_bucket     = "portfolio-media-elsewhere"
  }

  assert {
    condition     = google_storage_bucket_iam_member.media[0].bucket == "portfolio-media-elsewhere"
    error_message = "The grant must name the bucket in the other project."
  }

  assert {
    condition     = google_storage_bucket_iam_member.media[0].role == "roles/storage.objectUser"
    error_message = "The cross-project grant must still be object access only."
  }

  assert {
    condition     = { for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name => e.value if e.value != null }["MEDIA_GCS_BUCKET"] == "portfolio-media-elsewhere"
    error_message = "The api service must get the other project's bucket name."
  }

  assert {
    condition     = output.registry == "asia-southeast1-docker.pkg.dev/site-project-test/portfolio"
    error_message = "media_gcs_project_id must not move the site's own resources out of gcp_project_id."
  }
}

# --- media in an Azure container ---------------------------------------------------

# An Azure container reached with a SAS token (media_access_key = true, token
# passed in by the playbook): MEDIA_AZURE_SAS becomes an eighth secret, readable
# by the API service account, that reaches api and worker as a Secret Manager
# reference; account and container are plain variables; no bucket grant.
# media_gcs_bucket keeps the baseline bucket name (as after a move from Google
# to Azure), so "no bucket grant" and "no MEDIA_GCS_BUCKET" follow from
# media_storage, not from an empty bucket name.
run "azure_with_access_key" {
  command = plan

  variables {
    media_storage               = "azure"
    media_azure_account         = "portfoliomediatest"
    media_azure_container       = "media-files"
    media_azure_subscription_id = "00000000-0000-0000-0000-000000000000"
    media_access_key            = true
    media_azure_sas             = "sv=2099-01-01&sr=c&sp=racwdl&sig=dummy-not-a-signature"
  }

  assert {
    condition     = { for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name => e.value if e.value != null }["MEDIA_STORAGE"] == "azure"
    error_message = "The api service must get MEDIA_STORAGE=azure."
  }

  assert {
    condition     = { for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name => e.value if e.value != null }["MEDIA_AZURE_ACCOUNT"] == "portfoliomediatest"
    error_message = "The api service must get MEDIA_AZURE_ACCOUNT."
  }

  assert {
    condition     = { for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name => e.value if e.value != null }["MEDIA_AZURE_CONTAINER"] == "media-files"
    error_message = "The api service must get MEDIA_AZURE_CONTAINER."
  }

  assert {
    condition     = length([for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.name if e.name == "MEDIA_GCS_BUCKET"]) == 0
    error_message = "An azure site must not get MEDIA_GCS_BUCKET."
  }

  assert {
    condition     = google_secret_manager_secret.s["MEDIA_AZURE_SAS"].secret_id == "portfolio-media-azure-sas"
    error_message = "The SAS token must be stored as the portfolio-media-azure-sas secret."
  }

  assert {
    condition     = nonsensitive(google_secret_manager_secret_version.s["MEDIA_AZURE_SAS"].secret_data == var.media_azure_sas)
    error_message = "The stored secret must hold media_azure_sas."
  }

  assert {
    condition     = google_secret_manager_secret_iam_member.api["MEDIA_AZURE_SAS"].role == "roles/secretmanager.secretAccessor"
    error_message = "The SAS secret needs a secretAccessor grant."
  }

  assert {
    condition     = [for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.value_source[0].secret_key_ref[0].secret if e.name == "MEDIA_AZURE_SAS"] == ["portfolio-media-azure-sas"]
    error_message = "MEDIA_AZURE_SAS must reach the api service as a reference to the portfolio-media-azure-sas secret."
  }

  assert {
    condition     = [for e in google_cloud_run_v2_service.api["worker"].template[0].containers[0].env : e.value_source[0].secret_key_ref[0].secret if e.name == "MEDIA_AZURE_SAS"] == ["portfolio-media-azure-sas"]
    error_message = "MEDIA_AZURE_SAS must reach the worker service too."
  }

  assert {
    condition = nonsensitive(alltrue([for svc in google_cloud_run_v2_service.api : alltrue([
      for e in svc.template[0].containers[0].env : e.value != var.media_azure_sas
    ])]))
    error_message = "The SAS token may reach the api and worker only as a Secret Manager reference, never as a plain environment value."
  }

  assert {
    condition     = length(google_storage_bucket_iam_member.media) == 0
    error_message = "An azure site must not get a Google bucket grant, even with media_gcs_bucket still set."
  }
}

# --- preconditions -----------------------------------------------------------------

# media_storage = "local" would keep uploads on Cloud Run's throwaway disk:
# refused by the precondition on google_cloud_run_v2_service.api.
run "local_media_refused" {
  command = plan

  variables {
    media_storage    = "local"
    media_gcs_bucket = ""
  }

  expect_failures = [google_cloud_run_v2_service.api]
}

# Azure media without media_access_key, so no SAS token: refused by the
# precondition on google_secret_manager_secret_version.s.
run "azure_without_access_key_refused" {
  command = plan

  variables {
    media_storage       = "azure"
    media_azure_account = "portfoliomediatest"
    media_access_key    = false
  }

  expect_failures = [google_secret_manager_secret_version.s]
}

# media_access_key = true but a bare apply without the playbook, so the token
# never arrives: the same precondition checks the token itself, not the flag.
run "azure_access_key_without_sas_refused" {
  command = plan

  variables {
    media_storage       = "azure"
    media_azure_account = "portfoliomediatest"
    media_access_key    = true
    media_azure_sas     = ""
  }

  expect_failures = [google_secret_manager_secret_version.s]
}

# --- records for the dns/ root -----------------------------------------------------

# site_records lists the four Google front-end A and four AAAA addresses for
# the apex and the www CNAME to ghs.googlehosted.com, matching the two domain
# mappings; this is the shape the dns/ root's site_records variable takes.
run "site_records_for_dns_root" {
  command = plan

  assert {
    condition     = length(output.site_records) == 9
    error_message = "Expected 4 A, 4 AAAA and 1 CNAME record."
  }

  assert {
    condition     = toset([for r in output.site_records : r.content if r.type == "A" && r.name == "site.example"]) == toset(["216.239.32.21", "216.239.34.21", "216.239.36.21", "216.239.38.21"])
    error_message = "The apex A records must be the four Google front-end IPv4 addresses."
  }

  assert {
    condition     = toset([for r in output.site_records : r.content if r.type == "AAAA" && r.name == "site.example"]) == toset(["2001:4860:4802:32::15", "2001:4860:4802:34::15", "2001:4860:4802:36::15", "2001:4860:4802:38::15"])
    error_message = "The apex AAAA records must be the four Google front-end IPv6 addresses."
  }

  assert {
    condition     = [for r in output.site_records : r if r.type == "CNAME"] == [{ name = "www.site.example", type = "CNAME", content = "ghs.googlehosted.com" }]
    error_message = "www must be a CNAME to ghs.googlehosted.com."
  }

  assert {
    condition     = toset(keys(google_cloud_run_domain_mapping.site)) == toset(["site.example", "www.site.example"])
    error_message = "The apex and www must both be mapped to the web service."
  }

  assert {
    condition     = google_cloud_run_domain_mapping.site["www.site.example"].spec[0].route_name == "portfolio-web"
    error_message = "The domain mappings must route to the web service."
  }
}

# map_domain = false: no domain mappings, and no records for the dns/ root.
run "no_domain_mapping" {
  command = plan

  variables {
    map_domain = false
  }

  assert {
    condition     = length(output.site_records) == 0
    error_message = "Without a domain mapping there must be no site records."
  }

  assert {
    condition     = length(google_cloud_run_domain_mapping.site) == 0
    error_message = "map_domain = false must not create domain mappings."
  }
}

# --- variable validations ----------------------------------------------------------

# media_storage accepts only local, gcs or azure.
run "media_storage_unknown_value_rejected" {
  command = plan

  variables {
    media_storage = "s3"
  }

  expect_failures = [var.media_storage]
}

# media_storage = "gcs" needs media_gcs_bucket.
run "gcs_without_bucket_rejected" {
  command = plan

  variables {
    media_storage    = "gcs"
    media_gcs_bucket = ""
  }

  expect_failures = [var.media_storage]
}

# media_storage = "azure" needs media_azure_account.
run "azure_without_account_rejected" {
  command = plan

  variables {
    media_storage       = "azure"
    media_gcs_bucket    = ""
    media_azure_account = ""
    media_azure_sas     = "sv=2099-01-01&sig=dummy-not-a-signature"
  }

  expect_failures = [var.media_storage]
}

# Bucket names with dots (or capitals) are refused.
run "gcs_bucket_name_rejected" {
  command = plan

  variables {
    media_gcs_bucket = "media.site.example"
  }

  expect_failures = [var.media_gcs_bucket]
}

# Storage account names are 3-24 lowercase letters and digits.
run "azure_account_name_rejected" {
  command = plan

  variables {
    media_storage       = "azure"
    media_azure_account = "Portfolio-Media"
    media_azure_sas     = "sv=2099-01-01&sig=dummy-not-a-signature"
  }

  expect_failures = [var.media_azure_account]
}

# Container names are lowercase with single hyphens.
run "azure_container_name_rejected" {
  command = plan

  variables {
    media_storage         = "azure"
    media_azure_account   = "portfoliomediatest"
    media_azure_container = "Media--Files"
    media_azure_sas       = "sv=2099-01-01&sig=dummy-not-a-signature"
  }

  expect_failures = [var.media_azure_container]
}

# app_secret must be at least 32 characters.
run "short_app_secret_rejected" {
  command = plan

  variables {
    app_secret = "too-short"
  }

  expect_failures = [var.app_secret]
}

# The example value of the Brevo webhook secret is refused.
run "example_brevo_webhook_secret_rejected" {
  command = plan

  variables {
    email_brevo_webhook_secret = "REPLACE_with_a_long_random_string"
  }

  expect_failures = [var.email_brevo_webhook_secret]
}

# The example value of the Mailjet webhook secret is refused.
run "example_mailjet_webhook_secret_rejected" {
  command = plan

  variables {
    email_mailjet_webhook_secret = "REPLACE_with_a_long_random_string"
  }

  expect_failures = [var.email_mailjet_webhook_secret]
}

# --- identities (mocked apply; see the note at the top) ----------------------------

# A bucket in another project: the one bucket grant goes to the API service
# account, and both api and worker run as that account.
run "gcs_grant_goes_to_api_service_account" {
  command = apply

  variables {
    media_gcs_project_id = "media-project-test"
    media_gcs_bucket     = "portfolio-media-elsewhere"
  }

  assert {
    condition     = google_storage_bucket_iam_member.media[0].member == google_service_account.api.member
    error_message = "The bucket grant must go to the API service account."
  }

  assert {
    condition     = google_storage_bucket_iam_member.media[0].member == "serviceAccount:portfolio-api@site-project-test.iam.gserviceaccount.com"
    error_message = "The bucket grant must name the site project's API service account."
  }

  assert {
    condition     = alltrue([for k, svc in google_cloud_run_v2_service.api : svc.template[0].service_account == google_service_account.api.email])
    error_message = "Both api and worker must run as the API service account that holds the bucket grant."
  }
}

# Azure with a SAS token: the API service account may read the SAS secret, and
# the services pin the secret version that holds the token. The bucket name
# stays set, as right after a move from Google to Azure.
run "azure_sas_readable_by_api_service_account" {
  command = apply

  variables {
    media_storage       = "azure"
    media_azure_account = "portfoliomediatest"
    media_access_key    = true
    media_azure_sas     = "sv=2099-01-01&sr=c&sp=racwdl&sig=dummy-not-a-signature"
  }

  assert {
    condition     = google_secret_manager_secret_iam_member.api["MEDIA_AZURE_SAS"].member == "serviceAccount:portfolio-api@site-project-test.iam.gserviceaccount.com"
    error_message = "The API service account must be able to read the SAS secret."
  }

  assert {
    condition     = [for e in google_cloud_run_v2_service.api["api"].template[0].containers[0].env : e.value_source[0].secret_key_ref[0].version if e.name == "MEDIA_AZURE_SAS"] == [google_secret_manager_secret_version.s["MEDIA_AZURE_SAS"].version]
    error_message = "The api service must pin the secret version that holds the SAS token."
  }

  assert {
    condition     = length(google_storage_bucket_iam_member.media) == 0
    error_message = "Switching to azure must remove the Google bucket grant."
  }
}

# The API is IAM-only: the web service account is its only invoker and calls
# it by its URL; only the web service is open to allUsers; the worker takes
# internal traffic only.
run "api_reachable_only_by_web" {
  command = apply

  assert {
    condition     = google_cloud_run_v2_service_iam_member.web_calls_api.name == "portfolio-api" && google_cloud_run_v2_service_iam_member.web_calls_api.member == "serviceAccount:portfolio-web@site-project-test.iam.gserviceaccount.com"
    error_message = "Only the web service account may invoke the api service."
  }

  assert {
    condition     = google_cloud_run_v2_service_iam_member.web_public.name == "portfolio-web" && google_cloud_run_v2_service_iam_member.web_public.member == "allUsers"
    error_message = "allUsers may invoke the web service only."
  }

  assert {
    condition     = google_cloud_run_v2_service.api["worker"].ingress == "INGRESS_TRAFFIC_INTERNAL_ONLY"
    error_message = "The worker must not accept outside traffic."
  }

  assert {
    condition     = { for e in google_cloud_run_v2_service.web.template[0].containers[0].env : e.name => e.value }["API_ID_TOKEN_AUDIENCE"] == "https://portfolio-api-test-as.a.run.app"
    error_message = "The web service must use the api service's URL as the identity token audience."
  }
}

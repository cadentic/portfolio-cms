# Tests for the media-gcs root (the private media bucket on Google Cloud
# Storage). The google provider is mocked, so nothing reaches Google Cloud;
# every value below is a dummy, never a secret.
#
#   terraform -chdir=infra/terraform/media-gcs init -backend=false
#   terraform -chdir=infra/terraform/media-gcs test
#
# Most runs are plans. Terraform 1.9 fills computed attributes (a service
# account's member and name, a key's private_key) with the mock and override
# values only on apply. The bucket has prevent_destroy, so an apply that puts
# it in state cannot be torn down at the end of the file: the one apply run
# targets the service account key (and so the service account) and leaves the
# bucket out. It sits near the end, followed only by the plan that reads the
# service account it left in state.
#
# What these tests cannot show:
#   - Which project the bucket, the APIs and the service account land in. None
#     of them sets `project`; they take the provider's, which main.tf sets to
#     the same local as output.project. A mocked provider ignores its own
#     configuration, and the resources' project is unknown at plan, so only
#     output.project is asserted. (The project in the override values below is
#     a fixed mock value, not derived from the variables.)
#   - That no other resource exists. terraform test can only refer to resources
#     main.tf declares, so it cannot assert the absence of, say, a project-level
#     IAM member or a second bucket binding. main.tf declares neither (checked
#     by review): the runs below pin down the one binding it does declare.

mock_provider "google" {
  override_resource {
    target = google_service_account.site
    values = {
      email  = "portfolio-media@media-project-test.iam.gserviceaccount.com"
      member = "serviceAccount:portfolio-media@media-project-test.iam.gserviceaccount.com"
      name   = "projects/media-project-test/serviceAccounts/portfolio-media@media-project-test.iam.gserviceaccount.com"
    }
  }

  # Google returns the key file as base64 of pretty-printed (multi-line) JSON.
  override_resource {
    target = google_service_account_key.site
    values = {
      private_key = "ewogICJ0eXBlIjogInNlcnZpY2VfYWNjb3VudCIsCiAgInByb2plY3RfaWQiOiAibWVkaWEtcHJvamVjdC10ZXN0IiwKICAicHJpdmF0ZV9rZXlfaWQiOiAiZHVtbXkta2V5LWlkIiwKICAicHJpdmF0ZV9rZXkiOiAiLS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0tXG5EVU1NWVxuLS0tLS1FTkQgUFJJVkFURSBLRVktLS0tLVxuIiwKICAiY2xpZW50X2VtYWlsIjogInBvcnRmb2xpby1tZWRpYUBtZWRpYS1wcm9qZWN0LXRlc3QuaWFtLmdzZXJ2aWNlYWNjb3VudC5jb20iCn0K"
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

  # Baseline, as the deploy playbook runs this root from secret.tfvars: a
  # Google bucket in the site's own project and no key. Every run changes
  # only what its scenario needs.
  gcp_project_id   = "site-project-test"
  media_storage    = "gcs"
  media_gcs_bucket = "portfolio-media-test"
}

# --- the bucket --------------------------------------------------------------------

# The bucket is private and recoverable: named from media_gcs_bucket, public
# access prevention enforced, uniform bucket-level IAM (no object ACLs),
# never force-destroyed, and deleted objects kept for 7 days (604800 s).
run "private_bucket_settings" {
  command = plan

  assert {
    condition     = google_storage_bucket.media.name == "portfolio-media-test"
    error_message = "The bucket must be named from media_gcs_bucket."
  }

  assert {
    condition     = google_storage_bucket.media.public_access_prevention == "enforced"
    error_message = "Public access prevention must be enforced on the media bucket."
  }

  assert {
    condition     = google_storage_bucket.media.uniform_bucket_level_access == true
    error_message = "The media bucket must use uniform bucket-level access (no per-object ACLs)."
  }

  assert {
    condition     = google_storage_bucket.media.force_destroy == false
    error_message = "The media bucket must not be force-destroyed with its objects."
  }

  assert {
    condition     = google_storage_bucket.media.storage_class == "STANDARD"
    error_message = "The media bucket must use the STANDARD storage class."
  }

  assert {
    condition     = length(google_storage_bucket.media.soft_delete_policy) == 1 && google_storage_bucket.media.soft_delete_policy[0].retention_duration_seconds == 604800
    error_message = "Deleted or replaced media must stay recoverable for exactly 7 days (604800 seconds)."
  }
}

# With only gcp_project_id set, the bucket goes into the site's project in the
# default region, and the outputs name that bucket and project. The Storage
# and IAM APIs are enabled there and left on when the root is destroyed.
run "bucket_in_site_project" {
  command = plan

  assert {
    condition     = output.project == "site-project-test"
    error_message = "Without media_gcs_project_id the bucket must go into gcp_project_id."
  }

  assert {
    condition     = output.bucket == "portfolio-media-test"
    error_message = "The bucket output must be the bucket name."
  }

  assert {
    condition     = upper(google_storage_bucket.media.location) == "ASIA-SOUTHEAST1"
    error_message = "The bucket must default to media_gcs_location's default, asia-southeast1."
  }

  assert {
    condition     = toset(keys(google_project_service.apis)) == toset(["storage.googleapis.com", "iam.googleapis.com"])
    error_message = "Exactly the Storage and IAM APIs must be enabled."
  }

  assert {
    condition     = alltrue([for s in google_project_service.apis : s.disable_on_destroy == false])
    error_message = "The APIs must stay enabled when this root is destroyed (other roots share the project)."
  }
}

# media_gcs_project_id wins over gcp_project_id (a bucket in another Google
# Cloud project), and media_gcs_location sets the region.
run "bucket_in_media_project" {
  command = plan

  variables {
    media_gcs_project_id = "media-project-test"
    media_gcs_location   = "europe-west1"
  }

  assert {
    condition     = output.project == "media-project-test"
    error_message = "media_gcs_project_id must take precedence over gcp_project_id."
  }

  assert {
    condition     = upper(google_storage_bucket.media.location) == "EUROPE-WEST1"
    error_message = "The bucket location must come from media_gcs_location."
  }
}

# A site outside Google Cloud (VPS, Azure) may have no gcp_project_id at all:
# media_gcs_project_id alone is enough.
run "bucket_in_media_project_without_site_project" {
  command = plan

  variables {
    gcp_project_id       = ""
    media_gcs_project_id = "media-project-test"
  }

  assert {
    condition     = output.project == "media-project-test"
    error_message = "media_gcs_project_id alone must select the bucket's project."
  }

  assert {
    condition     = google_storage_bucket.media.name == "portfolio-media-test"
    error_message = "The bucket must still be planned, named from media_gcs_bucket."
  }
}

# --- the access key ----------------------------------------------------------------

# media_access_key = false (the default, for a site on Cloud Run): no service
# account, no binding, no key, and the credentials output the deploy playbook
# reads is empty.
run "no_access_key_by_default" {
  command = plan

  assert {
    condition     = length(google_service_account.site) == 0
    error_message = "No service account may be created without media_access_key."
  }

  assert {
    condition     = length(google_storage_bucket_iam_member.site) == 0
    error_message = "No bucket binding may be created without media_access_key."
  }

  assert {
    condition     = length(google_service_account_key.site) == 0
    error_message = "No service account key may be created without media_access_key."
  }

  assert {
    condition     = output.credentials == ""
    error_message = "The credentials output must be empty without media_access_key."
  }
}

# media_access_key = true: one dedicated service account, one key, and one
# binding whose scope is this bucket (a google_storage_bucket_iam_member on
# the media bucket, not a project-level member; see the header for what a
# test cannot rule out) with the object role only, so the key cannot change
# bucket settings or IAM. The member is unknown at plan; the last run checks it.
run "access_key_scoped_to_bucket" {
  command = plan

  variables {
    media_access_key = true
  }

  assert {
    condition     = length(google_service_account.site) == 1 && google_service_account.site[0].account_id == "portfolio-media"
    error_message = "media_access_key must create one service account, portfolio-media."
  }

  assert {
    condition     = strcontains(google_service_account.site[0].display_name, "bucket portfolio-media-test only")
    error_message = "The service account's display name must name the one bucket it can reach."
  }

  assert {
    condition     = length(google_storage_bucket_iam_member.site) == 1
    error_message = "media_access_key must create exactly one binding for the service account."
  }

  assert {
    condition     = google_storage_bucket_iam_member.site[0].bucket == "portfolio-media-test" && google_storage_bucket_iam_member.site[0].bucket == google_storage_bucket.media.name
    error_message = "The service account's binding must be scoped to the media bucket."
  }

  assert {
    condition     = google_storage_bucket_iam_member.site[0].role == "roles/storage.objectUser"
    error_message = "The service account must get object access only (roles/storage.objectUser)."
  }

  assert {
    condition     = length(google_service_account_key.site) == 1
    error_message = "media_access_key must create one service account key."
  }
}

# --- refused configurations: preconditions -----------------------------------------

# No bucket name (media_storage left at local, so the variable validation does
# not catch it first): the bucket's precondition stops the plan.
run "missing_bucket_name_refused" {
  command = plan

  variables {
    media_storage    = "local"
    media_gcs_bucket = ""
  }

  expect_failures = [
    google_storage_bucket.media,
  ]
}

# Neither media_gcs_project_id nor gcp_project_id: the bucket's precondition
# stops the plan instead of creating it in a project called "unset".
run "missing_project_refused" {
  command = plan

  variables {
    gcp_project_id       = ""
    media_gcs_project_id = ""
  }

  expect_failures = [
    google_storage_bucket.media,
  ]
}

# --- refused configurations: variable validations ----------------------------------

# media_storage = "gcs" without a bucket name is rejected by media_storage's
# validation. Terraform 1.9 keeps planning after a failed validation, so the
# bucket's precondition fails as well.
run "gcs_without_bucket_rejected" {
  command = plan

  variables {
    media_gcs_bucket = ""
  }

  expect_failures = [
    var.media_storage,
    google_storage_bucket.media,
  ]
}

# media_storage accepts only local, gcs or azure (lower case).
run "media_storage_unknown_value_rejected" {
  command = plan

  variables {
    media_storage = "GCS"
  }

  expect_failures = [
    var.media_storage,
  ]
}

# A dotted (domain-style) bucket name is rejected: names are 3-63 lowercase
# letters, digits, - and _.
run "bucket_name_with_dots_rejected" {
  command = plan

  variables {
    media_gcs_bucket = "media.site.example"
  }

  expect_failures = [
    var.media_gcs_bucket,
  ]
}

# A bucket name shorter than 3 characters is rejected.
run "bucket_name_too_short_rejected" {
  command = plan

  variables {
    media_gcs_bucket = "pm"
  }

  expect_failures = [
    var.media_gcs_bucket,
  ]
}

# A bucket name longer than 63 characters is rejected (64 here).
run "bucket_name_too_long_rejected" {
  command = plan

  variables {
    media_gcs_bucket = "portfolio-media-0123456789-0123456789-0123456789-0123456789-abcd"
  }

  expect_failures = [
    var.media_gcs_bucket,
  ]
}

# Google bucket names are lower case only: an upper-case letter is rejected.
run "bucket_name_uppercase_rejected" {
  command = plan

  variables {
    media_gcs_bucket = "Portfolio-Media-Test"
  }

  expect_failures = [
    var.media_gcs_bucket,
  ]
}

# A name must start (and end) with a letter or digit.
run "bucket_name_leading_hyphen_rejected" {
  command = plan

  variables {
    media_gcs_bucket = "-portfolio-media-test"
  }

  expect_failures = [
    var.media_gcs_bucket,
  ]
}

# The limits themselves are allowed, so the validation is not stricter than
# Google: 63 characters with - and _ ...
run "bucket_name_longest_accepted" {
  command = plan

  variables {
    media_gcs_bucket = "portfolio_media-0123456789-0123456789-0123456789-0123456789-abc"
  }

  assert {
    condition     = length(google_storage_bucket.media.name) == 63
    error_message = "A 63-character bucket name with - and _ must be accepted and planned."
  }
}

# ... and 3 characters.
run "bucket_name_shortest_accepted" {
  command = plan

  variables {
    media_gcs_bucket = "pm3"
  }

  assert {
    condition     = google_storage_bucket.media.name == "pm3"
    error_message = "A 3-character bucket name must be accepted and planned."
  }
}

# --- the key the deploy playbook reads (apply against the mock) --------------------

# The key belongs to the dedicated service account, and the credentials output
# (read with `terraform output -raw credentials`, then MEDIA_GCS_CREDENTIALS)
# is the decoded key file re-encoded as one line of JSON. Targets the key, so
# only the APIs, the service account and the key reach state, not the bucket.
run "access_key_credentials_output" {
  command = apply

  variables {
    media_access_key     = true
    media_gcs_project_id = "media-project-test"
  }

  plan_options {
    target = [google_service_account_key.site]
  }

  assert {
    condition     = google_service_account_key.site[0].service_account_id == google_service_account.site[0].name
    error_message = "The key must belong to the dedicated service account."
  }

  assert {
    condition     = !strcontains(nonsensitive(output.credentials), "\n")
    error_message = "The credentials output must be a single line."
  }

  assert {
    condition     = jsondecode(nonsensitive(output.credentials)) == jsondecode(base64decode(google_service_account_key.site[0].private_key))
    error_message = "The credentials output must hold the whole key file from private_key."
  }

  assert {
    condition     = jsondecode(nonsensitive(output.credentials)).client_email == "portfolio-media@media-project-test.iam.gserviceaccount.com"
    error_message = "The credentials output must be the key of the portfolio-media service account."
  }
}

# With the service account from the previous run in state, its member is known
# at plan: the bucket binding goes to that service account and nobody else.
run "access_key_binding_member_is_service_account" {
  command = plan

  variables {
    media_access_key     = true
    media_gcs_project_id = "media-project-test"
  }

  assert {
    condition     = google_storage_bucket_iam_member.site[0].member == "serviceAccount:portfolio-media@media-project-test.iam.gserviceaccount.com"
    error_message = "The bucket binding must be for the dedicated service account."
  }

  assert {
    condition     = google_storage_bucket_iam_member.site[0].member == google_service_account.site[0].member
    error_message = "The bucket binding's member must be the service account this root creates."
  }
}

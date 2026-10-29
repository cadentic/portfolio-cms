# Private Google Cloud Storage bucket for the site's media. It can live in its
# own project (media_gcs_project_id), apart from the Cloud Run project.
#
# Nothing here is public: public access prevention is enforced and IAM is
# uniform (no per-object ACLs). The site reaches the bucket in one of two ways:
#   - Cloud Run: the gcp/ root gives the API's service account object access
#     on this bucket only (no project-wide storage role).
#   - Anywhere else (VPS, Azure): with media_access_key = true this root creates
#     a service account whose only permission is object access on this bucket,
#     and a key for it, which the deploy playbook hands to the site.
terraform {
  required_version = ">= 1.9"
  required_providers {
    google = { source = "hashicorp/google", version = ">= 7.0, < 9.0" }
  }
  # State holds the access key. Use an encrypted remote backend, for example:
  # backend "gcs" {
  #   bucket = "cadentic-tfstate"
  #   prefix = "portfolio-cms/media-gcs"
  # }
}

variable "gcp_project_id" {
  type    = string
  default = ""
}

locals {
  project = coalesce(var.media_gcs_project_id, var.gcp_project_id, "unset")
}

provider "google" {
  project = local.project
}

resource "google_project_service" "apis" {
  for_each           = toset(["storage.googleapis.com", "iam.googleapis.com"])
  service            = each.value
  disable_on_destroy = false
}

resource "google_storage_bucket" "media" {
  name                        = var.media_gcs_bucket
  location                    = var.media_gcs_location
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  # Deleted or replaced files stay recoverable for 7 days.
  soft_delete_policy {
    retention_duration_seconds = 604800
  }
  lifecycle {
    prevent_destroy = true
    precondition {
      condition     = var.media_gcs_bucket != "" && local.project != "unset"
      error_message = "Set media_gcs_bucket and media_gcs_project_id (or gcp_project_id) in secret.tfvars."
    }
  }
  depends_on = [google_project_service.apis]
}

# --- key for a site outside Google Cloud ------------------------------------------

resource "google_service_account" "site" {
  count        = var.media_access_key ? 1 : 0
  account_id   = "portfolio-media"
  display_name = "portfolio-cms media (bucket ${var.media_gcs_bucket} only)"
  depends_on   = [google_project_service.apis]
}

# Create, read, replace and delete objects in this bucket. No bucket settings,
# no IAM, nothing outside the bucket.
resource "google_storage_bucket_iam_member" "site" {
  count  = var.media_access_key ? 1 : 0
  bucket = google_storage_bucket.media.name
  role   = "roles/storage.objectUser"
  member = google_service_account.site[0].member
}

resource "google_service_account_key" "site" {
  count              = var.media_access_key ? 1 : 0
  service_account_id = google_service_account.site[0].name
}

output "bucket" { value = google_storage_bucket.media.name }
output "project" { value = local.project }
output "url" { value = google_storage_bucket.media.url }
# The key as one line of JSON (MEDIA_GCS_CREDENTIALS), or "" without media_access_key.
output "credentials" {
  sensitive = true
  value     = var.media_access_key ? jsonencode(jsondecode(base64decode(google_service_account_key.site[0].private_key))) : ""
}

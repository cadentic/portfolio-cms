# Google Cloud Run deployment: web (public, Angular SSR), api (IAM-only; the
# web service calls it with an identity token) and worker (always-on queue
# worker, no public ingress). Database: Neon. DNS records live in the dns/
# root, which the deploy playbook points at this target's site_records.
terraform {
  required_version = ">= 1.9"
  required_providers {
    google = { source = "hashicorp/google", version = ">= 7.0, < 9.0" }
  }
  # State holds secret values. Use an encrypted remote backend, for example:
  # backend "gcs" {
  #   bucket = "cadentic-tfstate"
  #   prefix = "portfolio-cms/gcp"
  # }
}

variable "gcp_project_id" { type = string }
variable "gcp_region" {
  type        = string
  default     = "asia-southeast1"
  description = "Must support Cloud Run domain mappings (asia-south1 does not; asia-southeast1 does)."
}
variable "name" {
  type    = string
  default = "portfolio"
}
variable "map_domain" {
  type        = bool
  default     = true
  description = "Map the domain to the web service. Requires the domain to be verified in Google Search Console for the deploying account."
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
}

locals {
  site_url = "https://${var.domain}"
  # Every value here is stored in Secret Manager and mounted as an env var.
  secrets = merge({
    DATABASE_URL                 = var.database_url
    APP_SECRET                   = var.app_secret
    EMAIL_BREVO_API_KEY          = var.email_brevo_api_key
    EMAIL_BREVO_WEBHOOK_SECRET   = var.email_brevo_webhook_secret
    EMAIL_MAILJET_API_KEY        = var.email_mailjet_api_key
    EMAIL_MAILJET_SECRET_KEY     = var.email_mailjet_secret_key
    EMAIL_MAILJET_WEBHOOK_SECRET = var.email_mailjet_webhook_secret
    },
    # An Azure container is reached with a SAS token; a Google bucket with the
    # API's own service account (below), so no key.
    var.media_storage == "azure" ? { MEDIA_AZURE_SAS = var.media_azure_sas } : {},
  )
  api_env = merge(local.media_env, {
    APP_ENV                = "production"
    PUBLIC_SITE_URL        = local.site_url
    CORS_ALLOWED_ORIGINS   = local.site_url
    TRUST_CLIENT_IP_HEADER = "true" # only the web service can reach the API (IAM)
    FIREBASE_PROJECT_ID    = var.firebase_project_id
    ADMIN_EMAILS           = var.admin_emails
    ADMIN_UIDS             = var.admin_uids
    ADMIN_ALERT_EMAIL      = var.admin_alert_email
    EMAIL_FROM_ADDRESS     = var.email_from_address
    GTM_ID                 = var.gtm_id
    GA4_MEASUREMENT_ID     = var.ga4_measurement_id
  })
}

resource "google_project_service" "apis" {
  for_each           = toset(["run.googleapis.com", "artifactregistry.googleapis.com", "secretmanager.googleapis.com", "iam.googleapis.com", "storage.googleapis.com"])
  service            = each.value
  disable_on_destroy = false
}

resource "google_artifact_registry_repository" "images" {
  repository_id          = var.name
  format                 = "DOCKER"
  location               = var.gcp_region
  description            = "portfolio-cms images"
  cleanup_policy_dry_run = false
  cleanup_policies {
    id     = "delete-old"
    action = "DELETE"
    condition {
      tag_state  = "ANY"
      older_than = "2592000s" # 30 days; the 10 newest are kept by the rule below
    }
  }
  cleanup_policies {
    id     = "keep-recent"
    action = "KEEP"
    most_recent_versions { keep_count = 10 }
  }
  depends_on = [google_project_service.apis]
}

resource "google_service_account" "api" {
  account_id   = "${var.name}-api"
  display_name = "portfolio-cms API and worker"
}

resource "google_service_account" "web" {
  account_id   = "${var.name}-web"
  display_name = "portfolio-cms web (SSR)"
}

resource "google_secret_manager_secret" "s" {
  for_each  = nonsensitive(toset(keys(local.secrets)))
  secret_id = "${var.name}-${lower(replace(each.value, "_", "-"))}"
  replication {
    auto {}
  }
  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "s" {
  for_each    = google_secret_manager_secret.s
  secret      = each.value.id
  secret_data = local.secrets[each.key]
  lifecycle {
    create_before_destroy = true
    precondition {
      condition     = var.media_storage != "azure" || nonsensitive(var.media_azure_sas != "")
      error_message = "media_storage = \"azure\" on Cloud Run needs a SAS token: set media_access_key = true in secret.tfvars and deploy with the playbook, which passes the token in."
    }
  }
}

resource "google_secret_manager_secret_iam_member" "api" {
  for_each  = google_secret_manager_secret.s
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.api.email}"
}

# Media bucket (media-gcs/ root, possibly another project): the API and worker
# may create, read and delete objects in that one bucket, and nothing else.
resource "google_storage_bucket_iam_member" "media" {
  count      = var.media_storage == "gcs" ? 1 : 0
  bucket     = var.media_gcs_bucket
  role       = "roles/storage.objectUser"
  member     = google_service_account.api.member
  depends_on = [google_project_service.apis]
}

# --- API and worker share an image and configuration -----------------------------

resource "google_cloud_run_v2_service" "api" {
  for_each = { api = false, worker = true }
  name     = "${var.name}-${each.key}"
  location = var.gcp_region
  ingress  = each.value ? "INGRESS_TRAFFIC_INTERNAL_ONLY" : "INGRESS_TRAFFIC_ALL"
  # API traffic must carry an identity token; there is no allUsers invoker.
  deletion_protection = false

  template {
    service_account                  = google_service_account.api.email
    max_instance_request_concurrency = 80
    scaling {
      min_instance_count = each.value ? 1 : 0
      max_instance_count = each.value ? 2 : 4
    }
    containers {
      image = var.api_image
      ports { container_port = 8080 }
      resources {
        limits   = { cpu = "1", memory = "512Mi" }
        cpu_idle = !each.value # the worker polls the queue, so it needs CPU between requests
      }
      dynamic "env" {
        for_each = merge(local.api_env, { RUN_WORKER = each.value ? "true" : "false" })
        content {
          name  = env.key
          value = env.value
        }
      }
      dynamic "env" {
        for_each = google_secret_manager_secret.s
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret = env.value.secret_id
              # A pinned version: a changed secret rolls out a new revision.
              version = google_secret_manager_secret_version.s[env.key].version
            }
          }
        }
      }
      startup_probe {
        http_get { path = "/healthz" }
        period_seconds    = 3
        failure_threshold = 20
      }
      liveness_probe {
        http_get { path = "/healthz" }
        period_seconds = 30
      }
    }
  }
  lifecycle {
    precondition {
      condition     = var.media_storage != "local"
      error_message = "media_storage = \"local\" would keep uploads on the container's disk, which Cloud Run discards. Use a bucket: media_storage = \"gcs\" or \"azure\"."
    }
  }
  depends_on = [google_secret_manager_secret_version.s, google_secret_manager_secret_iam_member.api]
}

resource "google_cloud_run_v2_service_iam_member" "web_calls_api" {
  name     = google_cloud_run_v2_service.api["api"].name
  location = var.gcp_region
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.web.email}"
}

# --- web --------------------------------------------------------------------------

resource "google_cloud_run_v2_service" "web" {
  name                = "${var.name}-web"
  location            = var.gcp_region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false

  template {
    service_account = google_service_account.web.email
    scaling {
      min_instance_count = 0
      max_instance_count = 4
    }
    containers {
      image = var.web_image
      ports { container_port = 4000 }
      resources {
        limits = { cpu = "1", memory = "512Mi" }
      }
      dynamic "env" {
        for_each = {
          NODE_ENV              = "production"
          PUBLIC_SITE_URL       = local.site_url
          API_INTERNAL_URL      = google_cloud_run_v2_service.api["api"].uri
          API_ID_TOKEN_AUDIENCE = google_cloud_run_v2_service.api["api"].uri
          NG_ALLOWED_HOSTS      = "${var.domain},www.${var.domain},*.run.app"
          SSR_API_ORIGIN        = "http://127.0.0.1:4000"
          TRUSTED_PROXY_HOPS    = "1" # Google front end
          FIREBASE_API_KEY      = var.firebase_api_key
          FIREBASE_AUTH_DOMAIN  = var.firebase_auth_domain
          FIREBASE_PROJECT_ID   = var.firebase_project_id
        }
        content {
          name  = env.key
          value = env.value
        }
      }
      startup_probe {
        http_get { path = "/healthz" }
        period_seconds    = 3
        failure_threshold = 20
      }
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "web_public" {
  name     = google_cloud_run_v2_service.web.name
  location = var.gcp_region
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_domain_mapping" "site" {
  for_each = var.map_domain ? toset([var.domain, "www.${var.domain}"]) : toset([])
  name     = each.value
  location = var.gcp_region
  metadata { namespace = var.gcp_project_id }
  spec { route_name = google_cloud_run_v2_service.web.name }
}

# Cloud Run domain mappings always use these Google front-end addresses. The
# dns/ root creates the records; this root only describes them.
locals {
  site_records = var.map_domain ? concat(
    [for ip in ["216.239.32.21", "216.239.34.21", "216.239.36.21", "216.239.38.21"] : { name = var.domain, type = "A", content = ip }],
    [for ip in ["2001:4860:4802:32::15", "2001:4860:4802:34::15", "2001:4860:4802:36::15", "2001:4860:4802:38::15"] : { name = var.domain, type = "AAAA", content = ip }],
    [{ name = "www.${var.domain}", type = "CNAME", content = "ghs.googlehosted.com" }],
  ) : []
}

output "registry" {
  value = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${google_artifact_registry_repository.images.repository_id}"
}
output "web_url" { value = google_cloud_run_v2_service.web.uri }
output "site_url" { value = local.site_url }
output "webhook_urls" {
  value = { brevo = "${local.site_url}/api/webhooks/brevo", mailjet = "${local.site_url}/api/webhooks/mailjet" }
}
output "site_records" { value = local.site_records }

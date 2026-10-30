# Plan tests for the VPS root. The root has no providers and creates nothing:
# it renders the server's .env (the sensitive output env_file) and outputs the
# SSH details and the DNS records for the dns/ root. Every run is a plan and
# every value below is a dummy, never a secret.
#
#   terraform -chdir=infra/terraform/vps init -backend=false
#   terraform -chdir=infra/terraform/vps test
#
# The .env is checked line by line: contains(split("\n", ...), "KEY='value'")
# matches one whole line, so a key cannot match the tail of another key.

variables {
  domain                       = "portfolio.example.com"
  vps_ipv4                     = "203.0.113.10"
  database_url                 = "postgresql://dummy:dummy-neon-password@ep-dummy.neon.example.invalid/portfolio?sslmode=require"
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
}

# --- database ----------------------------------------------------------------------

# Defaults (Neon database, media on the server's volume): the whole .env, line
# for line. DATABASE_URL is database_url, COMPOSE_PROFILES is empty (no db
# container), POSTGRES_PASSWORD is empty, MEDIA_STORAGE is local and both media
# keys are empty. A leftover vps_db_password and leftover bucket keys never
# reach the file. MEDIA_LOCAL_DIR is not in the .env: compose.yml fixes it at
# /data/media, where the media volume is mounted.
run "neon_database_and_local_media" {
  command = plan

  variables {
    vps_db_password       = "leftover-password-0123456789"
    media_gcs_credentials = "leftover-gcs-key"
    media_azure_sas       = "leftover-azure-sas"
  }

  assert {
    condition     = nonsensitive(output.env_file) == <<-EOT
      ADMIN_ALERT_EMAIL=''
      ADMIN_EMAILS='admin@example.invalid'
      ADMIN_UIDS=''
      APP_SECRET='dummy-app-secret-for-tests-0123456789abcdef'
      COMPOSE_PROFILES=''
      DATABASE_URL='postgresql://dummy:dummy-neon-password@ep-dummy.neon.example.invalid/portfolio?sslmode=require'
      DOMAIN='portfolio.example.com'
      EMAIL_BREVO_API_KEY='dummy-brevo-api-key'
      EMAIL_BREVO_WEBHOOK_SECRET='dummy-brevo-webhook-secret'
      EMAIL_FROM_ADDRESS='newsletter@cadentic.dev'
      EMAIL_MAILJET_API_KEY='dummy-mailjet-api-key'
      EMAIL_MAILJET_SECRET_KEY='dummy-mailjet-secret-key'
      EMAIL_MAILJET_WEBHOOK_SECRET='dummy-mailjet-webhook-secret'
      FIREBASE_API_KEY='dummy-firebase-api-key'
      FIREBASE_AUTH_DOMAIN=''
      FIREBASE_PROJECT_ID='dummy-firebase-project'
      GA4_MEASUREMENT_ID=''
      GOOGLE_CONTACTS_ENABLED='false'
      GOOGLE_OAUTH_CLIENT_ID=''
      GOOGLE_OAUTH_CLIENT_SECRET=''
      GTM_ID=''
      MEDIA_AZURE_SAS=''
      MEDIA_GCS_CREDENTIALS=''
      MEDIA_STORAGE='local'
      POSTGRES_PASSWORD=''
      PUBLIC_SITE_URL='https://portfolio.example.com'
      SITE_ADDRESSES='portfolio.example.com www.portfolio.example.com'
      SITE_HOSTS='portfolio.example.com,www.portfolio.example.com'
    EOT
    error_message = "The default .env (Neon database, local media) does not match the expected 28 lines."
  }
  assert {
    condition     = !strcontains(nonsensitive(output.env_file), "MEDIA_LOCAL_DIR")
    error_message = "MEDIA_LOCAL_DIR belongs in compose.yml (the media volume's mount point), not in the .env."
  }
  assert {
    condition     = output.local_db == false
    error_message = "local_db must be false by default, so the playbook does not wait for a db container."
  }
}

# vps_local_db = true: DATABASE_URL points at the db container on port 5432
# with the password and sslmode=disable, COMPOSE_PROFILES = localdb starts that
# container, POSTGRES_PASSWORD is set, and the Neon database_url is not used.
run "local_database_container" {
  command = plan

  variables {
    vps_local_db    = true
    vps_db_password = "dummy0123456789abcdef0123456789ab"
  }

  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "DATABASE_URL='postgres://portfolio:dummy0123456789abcdef0123456789ab@db:5432/portfolio?sslmode=disable'")
    error_message = "DATABASE_URL must point at the db container (db:5432) with vps_db_password."
  }
  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "COMPOSE_PROFILES='localdb'")
    error_message = "COMPOSE_PROFILES must be localdb, so compose starts the db service."
  }
  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "POSTGRES_PASSWORD='dummy0123456789abcdef0123456789ab'")
    error_message = "POSTGRES_PASSWORD must be vps_db_password, so the db container and the API agree."
  }
  assert {
    condition     = !strcontains(nonsensitive(output.env_file), "neon.example.invalid")
    error_message = "With a local database the Neon database_url must not appear in the .env."
  }
  assert {
    condition     = output.local_db == true
    error_message = "local_db must be true, so the playbook knows the stack runs its own database."
  }
}

# A 16-character password is the shortest the precondition accepts.
run "local_database_password_16_characters" {
  command = plan

  variables {
    vps_local_db    = true
    vps_db_password = "abcdef0123456789"
  }

  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "POSTGRES_PASSWORD='abcdef0123456789'")
    error_message = "A 16-character password must be accepted and written to POSTGRES_PASSWORD."
  }
}

# vps_local_db = true without vps_db_password: refused by the precondition on
# output.env_file (the container would start with an empty password).
run "local_database_without_password" {
  command = plan

  variables {
    vps_local_db = true
  }

  expect_failures = [output.env_file]
}

# A 15-character password is too short: the same precondition refuses it.
run "local_database_short_password" {
  command = plan

  variables {
    vps_local_db    = true
    vps_db_password = "abcdef012345678"
  }

  expect_failures = [output.env_file]
}

# A password with "@", or the + / = of `openssl rand -base64`, would break the
# postgres:// URL it is pasted into: the same precondition refuses characters
# outside A-Z a-z 0-9 . _ ~ -. The value also holds a run of 16+ allowed
# characters, so the run fails if the regex loses its ^ or $ anchor.
run "local_database_password_breaks_url" {
  command = plan

  variables {
    vps_local_db    = true
    vps_db_password = "dummy@base64+password/0123456789abcdef=="
  }

  expect_failures = [output.env_file]
}

# --- media in a bucket -------------------------------------------------------------

# A Google bucket (possibly in another project) with a key limited to it, as
# the playbook passes it in: MEDIA_STORAGE=gcs, MEDIA_GCS_BUCKET and the key as
# one line of JSON in MEDIA_GCS_CREDENTIALS. A leftover Azure SAS stays out
# (MEDIA_AZURE_SAS is empty) and no MEDIA_AZURE_ACCOUNT or
# MEDIA_AZURE_CONTAINER is written.
run "gcs_bucket_with_key" {
  command = plan

  variables {
    media_storage         = "gcs"
    media_gcs_bucket      = "dummy-portfolio-media"
    media_gcs_project_id  = "dummy-media-project"
    media_access_key      = true
    media_gcs_credentials = "{\"type\":\"service_account\",\"project_id\":\"dummy-media-project\",\"private_key\":\"-----BEGIN PRIVATE KEY-----\\nDUMMY\\n-----END PRIVATE KEY-----\\n\",\"client_email\":\"dummy-media@dummy-media-project.iam.gserviceaccount.com\"}"
    media_azure_sas       = "leftover-azure-sas"
  }

  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "MEDIA_STORAGE='gcs'")
    error_message = "MEDIA_STORAGE must be gcs."
  }
  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "MEDIA_GCS_BUCKET='dummy-portfolio-media'")
    error_message = "MEDIA_GCS_BUCKET must be media_gcs_bucket."
  }
  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "MEDIA_GCS_CREDENTIALS='{\"type\":\"service_account\",\"project_id\":\"dummy-media-project\",\"private_key\":\"-----BEGIN PRIVATE KEY-----\\nDUMMY\\n-----END PRIVATE KEY-----\\n\",\"client_email\":\"dummy-media@dummy-media-project.iam.gserviceaccount.com\"}'")
    error_message = "MEDIA_GCS_CREDENTIALS must be the key passed in, as one line of JSON."
  }
  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "MEDIA_AZURE_SAS=''")
    error_message = "A gcs site must get an empty MEDIA_AZURE_SAS, even with a SAS left over."
  }
  assert {
    condition     = !strcontains(nonsensitive(output.env_file), "MEDIA_AZURE_ACCOUNT") && !strcontains(nonsensitive(output.env_file), "MEDIA_AZURE_CONTAINER")
    error_message = "A gcs site must not get MEDIA_AZURE_ACCOUNT or MEDIA_AZURE_CONTAINER."
  }
  assert {
    condition     = length(regexall("\n", nonsensitive(output.env_file))) == 29
    error_message = "A gcs .env has the 28 default lines plus MEDIA_GCS_BUCKET: 29 lines."
  }
}

# An Azure container (possibly in another subscription) with a SAS token
# limited to it: MEDIA_STORAGE=azure, MEDIA_AZURE_ACCOUNT, MEDIA_AZURE_CONTAINER
# and the token in MEDIA_AZURE_SAS. A leftover Google key stays out
# (MEDIA_GCS_CREDENTIALS is empty) and no MEDIA_GCS_BUCKET is written.
run "azure_container_with_sas" {
  command = plan

  variables {
    media_storage               = "azure"
    media_azure_account         = "dummymediaaccount"
    media_azure_container       = "site-media"
    media_azure_subscription_id = "00000000-0000-0000-0000-000000000002"
    media_access_key            = true
    media_azure_sas             = "sv=2022-11-02&sr=c&sp=racwdl&se=2027-01-01T00:00:00Z&sig=DUMMYSIGNATURE%3D"
    media_gcs_credentials       = "leftover-gcs-key"
  }

  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "MEDIA_STORAGE='azure'")
    error_message = "MEDIA_STORAGE must be azure."
  }
  assert {
    condition = alltrue([for line in ["MEDIA_AZURE_ACCOUNT='dummymediaaccount'", "MEDIA_AZURE_CONTAINER='site-media'"] :
      contains(split("\n", nonsensitive(output.env_file)), line)
    ])
    error_message = "MEDIA_AZURE_ACCOUNT and MEDIA_AZURE_CONTAINER must be media_azure_account and media_azure_container."
  }
  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "MEDIA_AZURE_SAS='sv=2022-11-02&sr=c&sp=racwdl&se=2027-01-01T00:00:00Z&sig=DUMMYSIGNATURE%3D'")
    error_message = "MEDIA_AZURE_SAS must be the SAS token passed in."
  }
  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "MEDIA_GCS_CREDENTIALS=''")
    error_message = "An azure site must get an empty MEDIA_GCS_CREDENTIALS, even with a Google key left over."
  }
  assert {
    condition     = !strcontains(nonsensitive(output.env_file), "MEDIA_GCS_BUCKET")
    error_message = "An azure site must not get MEDIA_GCS_BUCKET."
  }
  assert {
    condition     = length(regexall("\n", nonsensitive(output.env_file))) == 30
    error_message = "An azure .env has the 28 default lines plus MEDIA_AZURE_ACCOUNT and MEDIA_AZURE_CONTAINER: 30 lines."
  }
}

# A Google bucket without its key (media_access_key off, or a bare apply
# without the playbook): refused by the precondition on output.env_file.
run "gcs_bucket_without_key" {
  command = plan

  variables {
    media_storage    = "gcs"
    media_gcs_bucket = "dummy-portfolio-media"
  }

  expect_failures = [output.env_file]
}

# The same for an Azure container without its SAS token, here with
# media_access_key = true but no token passed in (a bare apply without the
# playbook): the flag alone is not enough, the precondition needs the token.
run "azure_container_without_sas" {
  command = plan

  variables {
    media_storage       = "azure"
    media_azure_account = "dummymediaaccount"
    media_access_key    = true
  }

  expect_failures = [output.env_file]
}

# The other cloud's key does not count: a Google bucket with only an Azure SAS
# left over is refused like a bucket without a key.
run "gcs_bucket_with_only_azure_sas" {
  command = plan

  variables {
    media_storage    = "gcs"
    media_gcs_bucket = "dummy-portfolio-media"
    media_azure_sas  = "leftover-azure-sas"
  }

  expect_failures = [output.env_file]
}

# --- values the .env cannot hold ---------------------------------------------------

# A single quote would end the single-quoted value early: refused by the
# precondition on output.env_file, also for a sensitive value (app_secret).
run "single_quote_in_value" {
  command = plan

  variables {
    app_secret = "dummy-app-secret-with-a-quote-'-0123456789"
  }

  expect_failures = [output.env_file]
}

# A line break would split the value over two lines (and could inject a
# setting): refused by the same precondition.
run "line_break_in_value" {
  command = plan

  variables {
    admin_emails = "admin@example.invalid\nINJECTED=1"
  }

  expect_failures = [output.env_file]
}

# A pretty-printed (multi-line) Google key is refused too; the playbook passes
# the key as one line of JSON.
run "multiline_gcs_key" {
  command = plan

  variables {
    media_storage         = "gcs"
    media_gcs_bucket      = "dummy-portfolio-media"
    media_access_key      = true
    media_gcs_credentials = "{\n  \"type\": \"service_account\",\n  \"project_id\": \"dummy-media-project\"\n}"
  }

  expect_failures = [output.env_file]
}

# --- server address, SSH and DNS records -------------------------------------------

# vps_ipv4 left empty (its default): refused by the precondition on
# output.env_file, so no A record without an address reaches the dns/ root.
run "missing_vps_ipv4" {
  command = plan

  variables {
    vps_ipv4 = ""
  }

  expect_failures = [output.env_file]
}

# A host name where the IPv4 address belongs is refused by the same precondition.
run "hostname_as_vps_ipv4" {
  command = plan

  variables {
    vps_ipv4 = "server.example.com"
  }

  expect_failures = [output.env_file]
}

# An IPv6 address in vps_ipv4 is refused too (it belongs in vps_ipv6).
run "ipv6_as_vps_ipv4" {
  command = plan

  variables {
    vps_ipv4 = "2001:db8::10"
  }

  expect_failures = [output.env_file]
}

# An address with a prefix length (as copied from a firewall rule) is refused
# too, so the run fails if the regex loses its $ anchor.
run "vps_ipv4_with_prefix_length" {
  command = plan

  variables {
    vps_ipv4 = "203.0.113.10/32"
  }

  expect_failures = [output.env_file]
}

# An address with octets over 255 has the right shape but is not an address.
run "out_of_range_vps_ipv4" {
  command = plan

  variables {
    vps_ipv4 = "256.300.999.1"
  }

  expect_failures = [output.env_file]
}

# vps_ipv6 must be an IPv6 address: neither text nor an IPv4 address becomes
# an AAAA record.
run "invalid_vps_ipv6" {
  command = plan

  variables {
    vps_ipv6 = "not-an-address"
  }

  expect_failures = [var.vps_ipv6]
}

run "ipv4_as_vps_ipv6" {
  command = plan

  variables {
    vps_ipv6 = "203.0.113.10"
  }

  expect_failures = [var.vps_ipv6]
}

# Without vps_ipv6: an apex A record and the www CNAME, nothing else. SSH goes
# to vps_ipv4 as root on port 22 into /opt/portfolio; images are linux/amd64.
run "defaults_ipv4_only" {
  command = plan

  assert {
    condition     = length(output.site_records) == 2
    error_message = "Without vps_ipv6 site_records must hold exactly two records (A and CNAME)."
  }
  assert {
    condition     = output.site_records[0] == { name = "portfolio.example.com", type = "A", content = "203.0.113.10" }
    error_message = "The first record must be the apex A record to vps_ipv4."
  }
  assert {
    condition     = output.site_records[1] == { name = "www.portfolio.example.com", type = "CNAME", content = "portfolio.example.com" }
    error_message = "The second record must be the www CNAME to the apex."
  }
  assert {
    condition     = output.ssh == { host = "203.0.113.10", user = "root", port = 22, dir = "/opt/portfolio" }
    error_message = "SSH must default to root@vps_ipv4:22 and /opt/portfolio."
  }
  assert {
    condition     = output.platform == "linux/amd64"
    error_message = "The image platform must default to linux/amd64."
  }
  assert {
    condition     = output.site_url == "https://portfolio.example.com" && output.web_url == output.site_url
    error_message = "site_url and web_url must both be https://<domain>."
  }
  assert {
    condition = output.webhook_urls == {
      brevo   = "https://portfolio.example.com/api/webhooks/brevo"
      mailjet = "https://portfolio.example.com/api/webhooks/mailjet"
    }
    error_message = "The webhook URLs must be the site's /api/webhooks/brevo and /api/webhooks/mailjet."
  }
}

# site_host on a subdomain: the records, Caddy and the site URL move to it,
# with no www (www.profile... is rarely wanted). DOMAIN stays the zone, where
# the mail records live.
run "subdomain_site_host" {
  command = plan

  variables {
    site_host = "Profile.Portfolio.Example.com"
    vps_ipv6  = "2001:db8::10"
  }

  assert {
    condition = output.site_records == [
      { name = "profile.portfolio.example.com", type = "A", content = "203.0.113.10" },
      { name = "profile.portfolio.example.com", type = "AAAA", content = "2001:db8::10" },
    ]
    error_message = "A subdomain site_host must get only its own A and AAAA records (lower-cased), no apex or www records."
  }
  assert {
    condition     = output.site_url == "https://profile.portfolio.example.com"
    error_message = "site_url must be https://<site_host>."
  }
  assert {
    condition     = output.webhook_urls.brevo == "https://profile.portfolio.example.com/api/webhooks/brevo"
    error_message = "The webhook URLs must use the site host."
  }
  assert {
    condition = alltrue([for line in [
      "DOMAIN='portfolio.example.com'",
      "PUBLIC_SITE_URL='https://profile.portfolio.example.com'",
      "SITE_ADDRESSES='profile.portfolio.example.com'",
      "SITE_HOSTS='profile.portfolio.example.com'",
    ] : contains(split("\n", nonsensitive(output.env_file)), line)])
    error_message = "The .env must keep DOMAIN on the zone and point PUBLIC_SITE_URL, SITE_ADDRESSES and SITE_HOSTS at the subdomain."
  }
}

# site_www = true on a subdomain adds www.<site_host> as a CNAME to it.
run "subdomain_with_www" {
  command = plan

  variables {
    site_host = "profile.portfolio.example.com"
    site_www  = true
  }

  assert {
    condition = output.site_records == [
      { name = "profile.portfolio.example.com", type = "A", content = "203.0.113.10" },
      { name = "www.profile.portfolio.example.com", type = "CNAME", content = "profile.portfolio.example.com" },
    ]
    error_message = "site_www = true must add the www CNAME to the subdomain."
  }
  assert {
    condition     = contains(split("\n", nonsensitive(output.env_file)), "SITE_ADDRESSES='profile.portfolio.example.com www.profile.portfolio.example.com'")
    error_message = "Caddy must serve both hostnames."
  }
}

# site_www = false on the apex drops the www CNAME.
run "apex_without_www" {
  command = plan

  variables {
    site_www = false
  }

  assert {
    condition     = output.site_records == [{ name = "portfolio.example.com", type = "A", content = "203.0.113.10" }]
    error_message = "site_www = false must leave only the apex A record."
  }
}

# A site_host outside the domain is refused: Terraform only manages the
# Cloudflare zone of the domain.
run "site_host_outside_domain" {
  command = plan

  variables {
    site_host = "profile.other.example"
  }

  expect_failures = [var.site_host]
}

run "site_host_suffix_without_dot" {
  command = plan

  variables {
    site_host = "notportfolio.example.com"
  }

  expect_failures = [var.site_host]
}

# vps_ipv6 adds an apex AAAA record between the A record and the www CNAME.
run "ipv6_adds_aaaa_record" {
  command = plan

  variables {
    vps_ipv6 = "2001:db8::10"
  }

  assert {
    condition     = length(output.site_records) == 3
    error_message = "With vps_ipv6 site_records must hold three records (A, AAAA and CNAME)."
  }
  assert {
    condition     = output.site_records[1] == { name = "portfolio.example.com", type = "AAAA", content = "2001:db8::10" }
    error_message = "The second record must be the apex AAAA record to vps_ipv6."
  }
  assert {
    condition     = [for r in output.site_records : r.type] == ["A", "AAAA", "CNAME"]
    error_message = "The records must be A, AAAA and CNAME, in that order."
  }
}

# A separate SSH address, user, port and directory (and an ARM server) pass
# through to the ssh and platform outputs; the A record still uses vps_ipv4.
run "ssh_settings_override" {
  command = plan

  variables {
    vps_ssh_host = "ssh.portfolio.example.com"
    vps_ssh_user = "deploy"
    vps_ssh_port = 2222
    vps_dir      = "/srv/portfolio"
    vps_platform = "linux/arm64"
  }

  assert {
    condition     = output.ssh == { host = "ssh.portfolio.example.com", user = "deploy", port = 2222, dir = "/srv/portfolio" }
    error_message = "The ssh output must carry vps_ssh_host, vps_ssh_user, vps_ssh_port and vps_dir."
  }
  assert {
    condition     = output.platform == "linux/arm64"
    error_message = "The platform output must be vps_platform."
  }
  assert {
    condition     = output.site_records[0].content == "203.0.113.10"
    error_message = "The A record must stay on vps_ipv4 when SSH uses another address."
  }
}

# --- variable validations ----------------------------------------------------------

# media_storage accepts only local, gcs or azure.
run "media_storage_unknown_value" {
  command = plan

  variables {
    media_storage = "s3"
  }

  expect_failures = [var.media_storage]
}

# media_storage = "gcs" needs media_gcs_bucket.
run "gcs_without_bucket" {
  command = plan

  variables {
    media_storage         = "gcs"
    media_gcs_credentials = "dummy-gcs-key"
  }

  expect_failures = [var.media_storage]
}

# media_storage = "azure" needs media_azure_account.
run "azure_without_account" {
  command = plan

  variables {
    media_storage   = "azure"
    media_azure_sas = "dummy-azure-sas"
  }

  expect_failures = [var.media_storage]
}

# A bucket name with dots or capitals is refused.
run "invalid_gcs_bucket_name" {
  command = plan

  variables {
    media_storage         = "gcs"
    media_gcs_bucket      = "Portfolio.Media"
    media_gcs_credentials = "dummy-gcs-key"
  }

  expect_failures = [var.media_gcs_bucket]
}

# A storage account name with a hyphen is refused.
run "invalid_azure_account_name" {
  command = plan

  variables {
    media_storage       = "azure"
    media_azure_account = "dummy-media"
    media_azure_sas     = "dummy-azure-sas"
  }

  expect_failures = [var.media_azure_account]
}

# A container name with a double hyphen is refused.
run "invalid_azure_container_name" {
  command = plan

  variables {
    media_storage         = "azure"
    media_azure_account   = "dummymediaaccount"
    media_azure_container = "site--media"
    media_azure_sas       = "dummy-azure-sas"
  }

  expect_failures = [var.media_azure_container]
}

# app_secret shorter than 32 characters is refused.
run "short_app_secret" {
  command = plan

  variables {
    app_secret = "too-short-dummy-secret"
  }

  expect_failures = [var.app_secret]
}

# app_secret still holding the example value (REPLACE_...) is refused.
run "example_app_secret" {
  command = plan

  variables {
    app_secret = "REPLACE_WITH_48_RANDOM_CHARACTERS_0123456789abcdef"
  }

  expect_failures = [var.app_secret]
}

# The Brevo webhook secret still holding the example value is refused.
run "example_brevo_webhook_secret" {
  command = plan

  variables {
    email_brevo_webhook_secret = "REPLACE_ME"
  }

  expect_failures = [var.email_brevo_webhook_secret]
}

# The Mailjet webhook secret still holding the example value is refused.
run "example_mailjet_webhook_secret" {
  command = plan

  variables {
    email_mailjet_webhook_secret = "REPLACE_ME"
  }

  expect_failures = [var.email_mailjet_webhook_secret]
}

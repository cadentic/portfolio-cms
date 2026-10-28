-- Portfolio CMS: subscribers, email delivery, content.
-- Runs on Neon PostgreSQL (and plain PostgreSQL 15+ locally).

CREATE EXTENSION IF NOT EXISTS citext;

-- ---------------------------------------------------------------------------
-- Settings (singleton rows keyed by name, typed by the Go settings package)
-- ---------------------------------------------------------------------------
CREATE TABLE app_settings (
    key         text PRIMARY KEY,
    value       jsonb NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  text
);

-- ---------------------------------------------------------------------------
-- Subscribers
-- ---------------------------------------------------------------------------
CREATE TABLE subscribers (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email             citext NOT NULL UNIQUE,
    name              text,
    status            text NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','verified','unsubscribed','bounced','complained','suppressed')),
    source            text NOT NULL DEFAULT 'website',
    verified_at       timestamptz,
    unsubscribed_at   timestamptz,
    last_verification_sent_at timestamptz,
    verification_send_count   int NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX subscribers_status_idx ON subscribers (status, created_at DESC);
CREATE INDEX subscribers_created_idx ON subscribers (created_at DESC);
CREATE INDEX subscribers_source_idx ON subscribers (source);

-- Single-use, time-limited verification tokens. Only the SHA-256 hash is stored.
CREATE TABLE subscriber_verification_tokens (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subscriber_id uuid NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
    token_hash    bytea NOT NULL UNIQUE,
    expires_at    timestamptz NOT NULL,
    used_at       timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX subscriber_verification_tokens_sub_idx ON subscriber_verification_tokens (subscriber_id);

-- Append-only consent evidence (grants, verifications, withdrawals).
CREATE TABLE subscriber_consents (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subscriber_id          uuid NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
    purpose                text NOT NULL DEFAULT 'marketing_newsletter',
    action                 text NOT NULL CHECK (action IN ('granted','verified','withdrawn','attested')),
    lawful_basis           text NOT NULL DEFAULT 'consent',
    source                 text NOT NULL,
    consent_text           text,
    privacy_policy_version text,
    ip                     inet,
    user_agent             text,
    evidence               jsonb NOT NULL DEFAULT '{}'::jsonb,
    actor                  text,
    created_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX subscriber_consents_sub_idx ON subscriber_consents (subscriber_id, created_at DESC);

CREATE TABLE subscriber_preferences (
    subscriber_id uuid PRIMARY KEY REFERENCES subscribers(id) ON DELETE CASCADE,
    topics        text[] NOT NULL DEFAULT '{}',
    frequency     text NOT NULL DEFAULT 'weekly',
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX subscriber_preferences_topics_idx ON subscriber_preferences USING gin (topics);

-- ---------------------------------------------------------------------------
-- Suppressions (scope 'marketing' blocks newsletters only; 'all' blocks everything)
-- ---------------------------------------------------------------------------
CREATE TABLE email_suppressions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email       citext NOT NULL,
    scope       text NOT NULL CHECK (scope IN ('marketing','all')),
    reason      text NOT NULL CHECK (reason IN ('unsubscribed','hard_bounce','complaint','manual','invalid','blocked')),
    source      text NOT NULL,
    provider_event_id uuid,
    note        text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    lifted_at   timestamptz,
    lifted_by   text
);
CREATE UNIQUE INDEX email_suppressions_active_uq ON email_suppressions (email, scope, reason) WHERE lifted_at IS NULL;
CREATE INDEX email_suppressions_email_idx ON email_suppressions (email) WHERE lifted_at IS NULL;

-- ---------------------------------------------------------------------------
-- Providers. Credentials are NEVER stored here: credential_refs holds the
-- names of server-side secrets (environment variables populated from the
-- cloud secret manager).
-- ---------------------------------------------------------------------------
CREATE TABLE email_providers (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  text NOT NULL UNIQUE,
    kind                  text NOT NULL,
    enabled               boolean NOT NULL DEFAULT false,
    priority              int NOT NULL DEFAULT 100,
    streams               text[] NOT NULL DEFAULT '{transactional,marketing}',
    credential_refs       jsonb NOT NULL DEFAULT '{}'::jsonb,
    webhook_secret_ref    text,
    sender_email          text,
    sender_name           text,
    reply_to              text,
    sending_domain        text,
    max_attempts          int NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 10),
    rate_limit_per_second numeric NOT NULL DEFAULT 5 CHECK (rate_limit_per_second > 0),
    settings              jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- circuit breaker (shared across worker instances)
    circuit_state         text NOT NULL DEFAULT 'closed' CHECK (circuit_state IN ('closed','open','half_open')),
    circuit_opened_at     timestamptz,
    consecutive_failures  int NOT NULL DEFAULT 0,
    -- health
    last_success_at       timestamptz,
    last_failure_at       timestamptz,
    last_error            text,
    last_rate_limited_at  timestamptz,
    last_health_check_at  timestamptz,
    last_health_ok        boolean,
    last_health_message   text,
    last_test_at          timestamptz,
    last_test_ok          boolean,
    quota_info            jsonb,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_providers_priority_idx ON email_providers (priority);

-- ---------------------------------------------------------------------------
-- Templates & campaigns
-- ---------------------------------------------------------------------------
CREATE TABLE email_templates (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        text NOT NULL UNIQUE,
    name        text NOT NULL,
    category    text NOT NULL CHECK (category IN ('transactional','marketing')),
    subject     text NOT NULL,
    preheader   text NOT NULL DEFAULT '',
    html_body   text NOT NULL,
    text_body   text NOT NULL DEFAULT '',
    is_system   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE email_campaigns (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL,
    kind            text NOT NULL DEFAULT 'newsletter' CHECK (kind IN ('newsletter','blog_notification','announcement')),
    status          text NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft','scheduled','sending','sent','cancelled','failed')),
    subject         text NOT NULL,
    preheader       text NOT NULL DEFAULT '',
    template_id     uuid REFERENCES email_templates(id) ON DELETE SET NULL,
    content_html    text NOT NULL DEFAULT '',
    content_text    text NOT NULL DEFAULT '',
    segment         jsonb NOT NULL DEFAULT '{}'::jsonb,
    blog_post_id    uuid,
    scheduled_at    timestamptz,
    approved_by     text,
    approved_at     timestamptz,
    audience_hash   text,
    audience_count  int,
    materialized_at timestamptz,
    send_started_at timestamptz,
    completed_at    timestamptz,
    created_by      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
-- One notification campaign per blog post, ever.
CREATE UNIQUE INDEX email_campaigns_blog_post_uq ON email_campaigns (blog_post_id) WHERE blog_post_id IS NOT NULL;
CREATE INDEX email_campaigns_status_idx ON email_campaigns (status, scheduled_at);

-- ---------------------------------------------------------------------------
-- Durable email queue
-- ---------------------------------------------------------------------------
CREATE TABLE email_jobs (
    id                   uuid PRIMARY KEY,               -- internal message ID
    idempotency_key      text NOT NULL UNIQUE,
    category             text NOT NULL CHECK (category IN ('transactional','marketing')),
    kind                 text NOT NULL,                  -- double_opt_in, campaign, test, alert, contact_ack, otp, ...
    status               text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued','processing','accepted','delivered','deferred','acceptance_unknown',
                                           'retry_scheduled','failed','bounced','complained','cancelled','dead_letter')),
    priority             int NOT NULL DEFAULT 0,
    campaign_id          uuid REFERENCES email_campaigns(id) ON DELETE SET NULL,
    subscriber_id        uuid REFERENCES subscribers(id) ON DELETE SET NULL,
    to_email             citext NOT NULL,
    to_name              text,
    from_email           text,
    from_name            text,
    reply_to             text,
    subject              text NOT NULL,
    html_body            text NOT NULL,
    text_body            text NOT NULL DEFAULT '',
    headers              jsonb NOT NULL DEFAULT '{}'::jsonb,
    tags                 text[] NOT NULL DEFAULT '{}',
    attempt_count        int NOT NULL DEFAULT 0,
    cycle                int NOT NULL DEFAULT 0,
    failover_count       int NOT NULL DEFAULT 0,
    current_provider_id  uuid REFERENCES email_providers(id) ON DELETE SET NULL,
    accepted_provider_id uuid REFERENCES email_providers(id) ON DELETE SET NULL,
    provider_message_id  text,
    next_attempt_at      timestamptz NOT NULL DEFAULT now(),
    lease_owner          text,
    lease_expires_at     timestamptz,
    last_error           text,
    unknown_since        timestamptz,
    reconcile_attempts   int NOT NULL DEFAULT 0,
    status_rank          int NOT NULL DEFAULT 0,
    last_event_at        timestamptz,
    accepted_at          timestamptz,
    delivered_at         timestamptz,
    finished_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_jobs_claim_idx ON email_jobs (next_attempt_at) WHERE status IN ('queued','retry_scheduled');
CREATE INDEX email_jobs_lease_idx ON email_jobs (lease_expires_at) WHERE status = 'processing';
CREATE INDEX email_jobs_unknown_idx ON email_jobs (next_attempt_at) WHERE status = 'acceptance_unknown';
CREATE INDEX email_jobs_status_idx ON email_jobs (status, created_at DESC);
CREATE INDEX email_jobs_campaign_idx ON email_jobs (campaign_id);
CREATE INDEX email_jobs_subscriber_idx ON email_jobs (subscriber_id, created_at DESC);
CREATE INDEX email_jobs_to_idx ON email_jobs (to_email);
CREATE INDEX email_jobs_provider_msg_idx ON email_jobs (provider_message_id) WHERE provider_message_id IS NOT NULL;

CREATE TABLE email_campaign_recipients (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id   uuid NOT NULL REFERENCES email_campaigns(id) ON DELETE CASCADE,
    subscriber_id uuid REFERENCES subscribers(id) ON DELETE SET NULL,
    email         citext NOT NULL,
    job_id        uuid REFERENCES email_jobs(id) ON DELETE SET NULL,
    opened_at     timestamptz,
    clicked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, email)
);
CREATE INDEX email_campaign_recipients_sub_idx ON email_campaign_recipients (subscriber_id);

CREATE TABLE email_attempts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id              uuid NOT NULL REFERENCES email_jobs(id) ON DELETE CASCADE,
    provider_id         uuid REFERENCES email_providers(id) ON DELETE SET NULL,
    provider_kind       text NOT NULL,
    provider_name       text NOT NULL,
    attempt_number      int NOT NULL,
    provider_attempt    int NOT NULL,
    cycle               int NOT NULL,
    worker_id           text NOT NULL,
    -- NULL outcome = request in flight. A crash leaves it NULL, which recovery
    -- treats as acceptance_unknown (never as "not sent").
    outcome             text CHECK (outcome IN ('accepted','rejected_transient','rejected_permanent','rate_limited',
                                                'not_sent','acceptance_unknown','interrupted')),
    http_status         int,
    provider_message_id text,
    error_code          text,
    error_message       text,
    response            jsonb,
    retry_after_ms      int,
    failover_reason     text,
    started_at          timestamptz NOT NULL DEFAULT now(),
    finished_at         timestamptz,
    UNIQUE (job_id, attempt_number)
);
CREATE INDEX email_attempts_job_idx ON email_attempts (job_id, attempt_number);
CREATE INDEX email_attempts_provider_idx ON email_attempts (provider_id, started_at DESC);
CREATE INDEX email_attempts_started_idx ON email_attempts (started_at DESC);

CREATE TABLE email_provider_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id         uuid REFERENCES email_providers(id) ON DELETE SET NULL,
    provider_kind       text NOT NULL,
    dedupe_key          text NOT NULL,
    provider_event_id   text,
    provider_message_id text,
    job_id              uuid REFERENCES email_jobs(id) ON DELETE SET NULL,
    email               citext,
    event_type          text NOT NULL CHECK (event_type IN ('accepted','delivered','deferred','soft_bounce','hard_bounce',
                                                              'complaint','unsubscribed','opened','clicked','blocked','invalid','error','unknown')),
    raw_event           text NOT NULL,
    occurred_at         timestamptz NOT NULL,
    received_at         timestamptz NOT NULL DEFAULT now(),
    processed_at        timestamptz,
    process_error       text,
    payload             jsonb NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (provider_kind, dedupe_key)
);
CREATE INDEX email_provider_events_job_idx ON email_provider_events (job_id, occurred_at);
CREATE INDEX email_provider_events_unprocessed_idx ON email_provider_events (received_at) WHERE processed_at IS NULL;
CREATE INDEX email_provider_events_type_idx ON email_provider_events (event_type, occurred_at DESC);

CREATE TABLE email_audit_logs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor        text NOT NULL,
    action       text NOT NULL,
    entity_type  text NOT NULL,
    entity_id    text,
    details      jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip           inet,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_audit_logs_created_idx ON email_audit_logs (created_at DESC);
CREATE INDEX email_audit_logs_entity_idx ON email_audit_logs (entity_type, entity_id);

CREATE TABLE admin_alerts (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dedupe_key       text NOT NULL,
    severity         text NOT NULL CHECK (severity IN ('info','warning','critical')),
    kind             text NOT NULL,
    message          text NOT NULL,
    details          jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurrences      int NOT NULL DEFAULT 1,
    first_seen_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at     timestamptz NOT NULL DEFAULT now(),
    notified_at      timestamptz,
    acknowledged_at  timestamptz,
    acknowledged_by  text
);
CREATE UNIQUE INDEX admin_alerts_open_uq ON admin_alerts (dedupe_key) WHERE acknowledged_at IS NULL;

-- Fixed-window rate-limit counters shared by all API instances.
CREATE TABLE rate_limit_counters (
    key          text NOT NULL,
    window_start timestamptz NOT NULL,
    count        int NOT NULL DEFAULT 0,
    PRIMARY KEY (key, window_start)
);
CREATE INDEX rate_limit_counters_window_idx ON rate_limit_counters (window_start);

-- CSV import staging (validated rows are held until the owner confirms).
CREATE TABLE subscriber_import_batches (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash   bytea NOT NULL UNIQUE,
    rows         jsonb NOT NULL,
    summary      jsonb NOT NULL,
    created_by   text NOT NULL,
    expires_at   timestamptz NOT NULL,
    committed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Content (no-code pages & articles)
-- ---------------------------------------------------------------------------
CREATE TABLE cms_pages (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind               text NOT NULL CHECK (kind IN ('page','article')),
    parent_id          uuid REFERENCES cms_pages(id) ON DELETE SET NULL,  -- page hierarchy
    menu_order         int NOT NULL DEFAULT 0,
    slug               text NOT NULL,
    title              text NOT NULL,
    excerpt            text NOT NULL DEFAULT '',
    -- Top section: {"type":"none"|"image"|"video"|"youtube", "url", "youtube_id", "alt", "caption", "poster"}
    hero               jsonb NOT NULL DEFAULT '{"type":"none"}'::jsonb,
    -- Body: ordered text blocks, e.g. [{"type":"heading","level":2,"text":"..."},
    -- {"type":"paragraph","text":"..."}, {"type":"list","style":"bullet","items":[...]},
    -- {"type":"quote","text":"...","cite":"..."}, {"type":"code","language":"sql","text":"..."},
    -- {"type":"callout","tone":"info","text":"..."}, {"type":"divider"},
    -- {"type":"html","html":"<sanitized>"}  (used for imported legacy content)
    blocks             jsonb NOT NULL DEFAULT '[]'::jsonb,
    featured_image_url text,
    author_name        text,
    categories         text[] NOT NULL DEFAULT '{}',
    tags               text[] NOT NULL DEFAULT '{}',
    seo                jsonb NOT NULL DEFAULT '{}'::jsonb,   -- {"title","description","og_image","canonical","noindex"}
    status             text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
    published_at       timestamptz,
    first_published_at timestamptz,
    source             text,          -- e.g. 'wordpress' for imported content
    source_id          text,
    created_by         text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, slug)
);
CREATE UNIQUE INDEX cms_pages_source_uq ON cms_pages (source, source_id) WHERE source IS NOT NULL;
CREATE INDEX cms_pages_parent_idx ON cms_pages (parent_id, menu_order);

CREATE TABLE cms_media (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    url         text NOT NULL,
    kind        text NOT NULL DEFAULT 'image' CHECK (kind IN ('image','video','file')),
    alt         text NOT NULL DEFAULT '',
    caption     text NOT NULL DEFAULT '',
    width       int,
    height      int,
    source      text,
    source_id   text,
    source_url  text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX cms_media_source_uq ON cms_media (source, source_id) WHERE source IS NOT NULL;

CREATE TABLE cms_redirects (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    from_path   text NOT NULL UNIQUE,
    to_path     text NOT NULL,
    status_code int NOT NULL DEFAULT 301 CHECK (status_code IN (301,302,307,308)),
    source      text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX cms_pages_published_idx ON cms_pages (kind, status, published_at DESC);

-- ---------------------------------------------------------------------------
-- Contact form & resume OTP (transactional email sources)
-- ---------------------------------------------------------------------------
CREATE TABLE contact_messages (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    email       citext NOT NULL,
    message     text NOT NULL,
    ip          inet,
    status      text NOT NULL DEFAULT 'new' CHECK (status IN ('new','read','archived')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE resume_otps (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email       citext NOT NULL,
    code_hash   bytea NOT NULL,
    expires_at  timestamptz NOT NULL,
    attempts    int NOT NULL DEFAULT 0,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX resume_otps_email_idx ON resume_otps (email, created_at DESC);

-- ---------------------------------------------------------------------------
-- Marketing eligibility: verified, consent not withdrawn, not suppressed.
-- ---------------------------------------------------------------------------
CREATE VIEW eligible_marketing_subscribers AS
SELECT s.*
FROM subscribers s
WHERE s.status = 'verified'
  AND EXISTS (
        SELECT 1 FROM subscriber_consents c
        WHERE c.subscriber_id = s.id AND c.purpose = 'marketing_newsletter'
          AND c.action IN ('verified','attested'))
  AND NOT EXISTS (
        SELECT 1 FROM subscriber_consents w
        WHERE w.subscriber_id = s.id AND w.purpose = 'marketing_newsletter' AND w.action = 'withdrawn'
          AND w.created_at > (SELECT max(c2.created_at) FROM subscriber_consents c2
                              WHERE c2.subscriber_id = s.id AND c2.action IN ('verified','attested')))
  AND NOT EXISTS (
        SELECT 1 FROM email_suppressions x
        WHERE x.email = s.email AND x.lifted_at IS NULL);

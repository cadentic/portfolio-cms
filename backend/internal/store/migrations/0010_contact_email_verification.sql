-- Contact form senders verify their email with a one-time code before a
-- message is accepted. The codes share the resume code table; purpose keeps
-- the two flows apart.
ALTER TABLE resume_otps ADD COLUMN purpose text NOT NULL DEFAULT 'resume'
    CHECK (purpose IN ('resume', 'contact'));

CREATE INDEX resume_otps_purpose_email_idx ON resume_otps (purpose, email, created_at DESC);

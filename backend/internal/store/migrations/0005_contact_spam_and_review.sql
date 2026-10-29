-- Blocked words for the contact form spam filter. A submission whose name or
-- message contains one of these words or phrases (case-insensitive, whole
-- words only) is rejected and the sender is told to stop. The owner edits the
-- list from the admin "Contact messages" page.
CREATE TABLE contact_blocked_words (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    word        text NOT NULL CHECK (length(word) BETWEEN 1 AND 100),
    hits        integer NOT NULL DEFAULT 0,
    last_hit_at timestamptz,
    created_by  text NOT NULL DEFAULT 'system',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX contact_blocked_words_word_idx ON contact_blocked_words (lower(word));

-- A small starter list of common contact-form spam. Remove or extend it in the admin.
INSERT INTO contact_blocked_words (word) VALUES
    ('viagra'), ('cialis'), ('casino'), ('porn'), ('escort'),
    ('backlinks'), ('seo ranking'), ('crypto investment'), ('forex signals'),
    ('loan offer'), ('bitcoin doubler')
ON CONFLICT DO NOTHING;

-- Contact messages are held in the admin for review. The owner forwards a
-- message to their inbox (queued as the contact_notify email) from there.
ALTER TABLE contact_messages
    ADD COLUMN forwarded_at timestamptz,
    ADD COLUMN forwarded_by text;

-- Messages received before this change were already emailed to the owner.
UPDATE contact_messages SET forwarded_at = created_at, forwarded_by = 'system' WHERE forwarded_at IS NULL;

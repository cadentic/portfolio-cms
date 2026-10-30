-- Legal pages become CMS pages (edited in Admin → Site content → Legal pages). The text is
-- the version published on 28 October 2026 (frontend legal.component.ts at 4c14b3e), as
-- blocks: links and bold become inline Markdown and the privacy table one html block. One
-- change: the AI policy "Content management" bullet now also covers pages, articles and site
-- text edited through MCP, which go live without a separate approval step.
-- Idempotent: ON CONFLICT DO NOTHING covers UNIQUE (kind, slug) and cms_pages_source_uq, and a
-- seed version is written only for a page this statement created.
WITH ins AS (
  INSERT INTO cms_pages (kind, slug, title, excerpt, hero, blocks, author_name, seo, status,
    published_at, first_published_at, source, source_id, created_by, created_at, updated_at)
  VALUES
  ('page', 'privacy', 'Privacy notice', '', '{"type":"none"}', $privacy$[
  {"type": "paragraph", "text": "This notice explains how Sayantan Chakraborty (\"I\", \"me\"), as the Data Fiduciary for this website (cadentic.dev), collects and uses your personal data under India's Digital Personal Data Protection Act, 2023 (DPDP Act) and the DPDP Rules, 2025. I only process personal data for the specific purposes below, with your consent or for a legitimate use permitted by the Act."},
  {"type": "heading", "level": 2, "text": "What I collect and why"},
  {"type": "html", "html": "<table><thead><tr><th>Purpose</th><th>Data</th><th>Basis</th><th>Kept for</th></tr></thead><tbody><tr><td>Newsletter</td><td>Email, optional name, chosen topics, consent record (time, wording version, IP address, browser)</td><td>Your consent, confirmed by email (double opt-in)</td><td>Until you unsubscribe; unconfirmed sign-ups are deleted after 30 days</td></tr><tr><td>Resume download</td><td>Email address and a one-time code</td><td>Consent, to send the code</td><td>30 days</td></tr><tr><td>Contact form</td><td>Name, email, message</td><td>Consent, to reply to you</td><td>24 months, then deleted</td></tr><tr><td>Email delivery records</td><td>Delivery status, bounces, complaints, unsubscribe requests</td><td>Legitimate use: honouring opt-outs and keeping mail deliverable</td><td>Message content 90 days; suppression records as long as needed to respect your opt-out</td></tr><tr><td>Analytics (optional)</td><td>Pseudonymous usage data via Google Analytics</td><td>Consent through the cookie banner; off by default</td><td>14 months (Google Analytics setting)</td></tr><tr><td>Security</td><td>IP address and request logs, rate-limit counters</td><td>Legitimate use: preventing abuse</td><td>Up to 30 days</td></tr></tbody></table>"},
  {"type": "paragraph", "text": "I do not sell personal data, use it for advertising, or knowingly collect data from children. If you are under 18, please do not submit the forms."},
  {"type": "heading", "level": 2, "text": "Who processes data for me"},
  {"type": "paragraph", "text": "I use Data Processors bound by contract: Brevo (Sendinblue SAS, EU) and Mailjet (Sinch, EU) to send email; Neon (PostgreSQL hosting) for the database; Google Cloud / Firebase or Microsoft Azure for hosting and owner sign-in; Cloudflare for DNS and network security; Google Analytics only if you allow it. Some processing happens outside India, which the DPDP Act permits except for countries the Government restricts."},
  {"type": "heading", "level": 2, "text": "Your rights"},
  {"type": "list", "style": "bullet", "items": ["**Withdraw consent** at any time: every newsletter has a one-click unsubscribe link, and you can change analytics consent from \"Cookie settings\" in the footer. Withdrawal is as easy as giving consent.", "**Access** a summary of your personal data and how it was processed.", "**Correction, completion, updating and erasure** of your data.", "**Grievance redressal**: I will respond within 30 days at the latest.", "**Nominate** another person to exercise your rights in case of death or incapacity."]},
  {"type": "paragraph", "text": "To exercise any right, use the [contact form](/#contact) with the subject \"Data request\". I may need to verify your identity. If you are not satisfied with my response, you can complain to the Data Protection Board of India."},
  {"type": "heading", "level": 2, "text": "Security and breaches"},
  {"type": "paragraph", "text": "Data is encrypted in transit, access to the admin area requires authenticated sign-in, API keys are kept in a secret manager, and tokens are stored only as hashes. If a personal data breach affects you, I will inform you and the Data Protection Board as the Rules require."},
  {"type": "heading", "level": 2, "text": "Contact"},
  {"type": "paragraph", "text": "Data Fiduciary and grievance contact: Sayantan Chakraborty, Bangalore, India, via the [contact form](/#contact)."}
]$privacy$::jsonb, 'Sayantan Chakraborty',
   '{"description": "How personal data is collected, used and protected on cadentic.dev under the DPDP Act 2023 and DPDP Rules 2025."}',
   'published', '2026-10-28 12:00:00+05:30', '2026-10-28 12:00:00+05:30', 'system', 'privacy', 'migration', '2026-10-28 12:00:00+05:30', '2026-10-28 12:00:00+05:30'),
  ('page', 'terms', 'Terms of use', '', '{"type":"none"}', $terms$[
  {"type": "paragraph", "text": "By using cadentic.dev you agree to these terms. If you don't agree, please don't use the site."},
  {"type": "heading", "level": 2, "text": "Content"},
  {"type": "paragraph", "text": "Articles, videos and code samples are shared for general information about database administration. They are provided \"as is\", without warranty. Test any command, script or configuration in a non-production environment and follow your organisation's change-management process before applying it. I am not liable for loss arising from use of the content."},
  {"type": "heading", "level": 2, "text": "Intellectual property"},
  {"type": "paragraph", "text": "Unless stated otherwise, the text, images and videos on this site are © Sayantan Chakraborty. You may quote short extracts with attribution and a link. Code snippets may be reused in your own work. Oracle, Oracle Database, RAC, Data Guard, GoldenGate and other product names are trademarks of their respective owners; no endorsement is implied. Organisation names shown in the career section describe past employment or engagements only."},
  {"type": "heading", "level": 2, "text": "Acceptable use"},
  {"type": "paragraph", "text": "Don't attempt to disrupt the site, probe it for vulnerabilities without permission, scrape it at a rate that degrades service, or submit unlawful, abusive or misleading content through the forms. Automated requests are rate limited."},
  {"type": "heading", "level": 2, "text": "Newsletter and resume"},
  {"type": "paragraph", "text": "The newsletter is free and you can leave at any time. The resume is shared for recruitment and professional enquiries only; please don't republish it."},
  {"type": "heading", "level": 2, "text": "Links"},
  {"type": "paragraph", "text": "Links to third-party sites (YouTube, LinkedIn, GitHub and others) are provided for convenience; I'm not responsible for their content or privacy practices."},
  {"type": "heading", "level": 2, "text": "Governing law"},
  {"type": "paragraph", "text": "These terms are governed by the laws of India, and the courts of Bengaluru, Karnataka have exclusive jurisdiction."},
  {"type": "heading", "level": 2, "text": "Changes"},
  {"type": "paragraph", "text": "I may update these terms; the date above shows the latest version. See also the [privacy notice](/privacy) and [AI policy](/ai-policy)."}
]$terms$::jsonb, 'Sayantan Chakraborty',
   '{"description": "Terms of use for cadentic.dev: content, intellectual property, acceptable use and governing law."}',
   'published', '2026-10-28 12:00:00+05:30', '2026-10-28 12:00:00+05:30', 'system', 'terms', 'migration', '2026-10-28 12:00:00+05:30', '2026-10-28 12:00:00+05:30'),
  ('page', 'ai-policy', 'AI policy', '', '{"type":"none"}', $aipolicy$[
  {"type": "paragraph", "text": "This page explains how artificial intelligence is used on and around this site, and what I ask of AI systems that read it."},
  {"type": "heading", "level": 2, "text": "How I use AI"},
  {"type": "list", "style": "bullet", "items": ["**Writing assistance.** I sometimes use AI tools to draft, outline or proofread articles. Every published article is reviewed, tested and edited by me, and I am responsible for its accuracy.", "**Content management.** I can create and edit pages, articles and site text through an AI assistant connected to this site's CMS (Model Context Protocol). It acts only on my instructions, requires my sign-in, can't send email to subscribers or change these legal pages, and every change it makes is recorded in an audit log. Newsletters go out only after I approve them in the CMS.", "**No automated decisions about you.** AI is not used to profile subscribers, make decisions about visitors, or process your personal data beyond the purposes in the [privacy notice](/privacy).", "**Commands and code.** Database commands in articles are tested by me; AI-generated snippets are labelled when they haven't been run against a real system."]},
  {"type": "heading", "level": 2, "text": "AI crawlers and answer engines"},
  {"type": "paragraph", "text": "Search and answer engines (including AI assistants) may read and cite public articles. A machine-readable summary is at [/llms.txt](/llms.txt), and permissions are in [/robots.txt](/robots.txt). When you quote or summarise content, please attribute \"Sayantan Chakraborty\" and link to the original article. The admin area, APIs and personal data are excluded and must not be crawled."},
  {"type": "heading", "level": 2, "text": "Training"},
  {"type": "paragraph", "text": "You may index and cite public articles. Please do not use the resume, contact details or any personal data on this site to train models or build profiles."},
  {"type": "heading", "level": 2, "text": "Questions"},
  {"type": "paragraph", "text": "Questions or concerns about AI use: please get in touch through the [contact form](/#contact)."}
]$aipolicy$::jsonb, 'Sayantan Chakraborty',
   '{"description": "How AI is used for writing and content management on cadentic.dev, and guidance for AI crawlers."}',
   'published', '2026-10-28 12:00:00+05:30', '2026-10-28 12:00:00+05:30', 'system', 'ai-policy', 'migration', '2026-10-28 12:00:00+05:30', '2026-10-28 12:00:00+05:30')
  ON CONFLICT DO NOTHING
  RETURNING id, title, excerpt, hero, blocks, seo
)
INSERT INTO content_versions (entity_type, entity_id, action, snapshot, sha256, actor, created_at)
SELECT 'page', id::text, 'seed', s.snap, encode(sha256(convert_to(s.snap::text, 'UTF8')), 'hex'), 'migration', '2026-10-28 12:00:00+05:30'
FROM ins, LATERAL (SELECT jsonb_build_object('title', title, 'excerpt', excerpt, 'hero', hero, 'blocks', blocks, 'seo', seo) AS snap) s;

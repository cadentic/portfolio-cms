-- More blocked phrases for the contact form, covering the spam the owner
-- gets most: "ethical hacker" bug-bounty begging ("beg bounty"), prize-money
-- and lottery scams, and SEO / marketing pitches. The owner does no SEO work,
-- so the word "seo" itself is blocked too. The owner can remove or extend these on the
-- admin Contact messages page. Matching is whole words, case-insensitive.
INSERT INTO contact_blocked_words (word) VALUES
    -- bug-bounty begging
    ('ethical hacker'), ('ethical hacking'), ('bug bounty'), ('beg bounty'),
    ('bounty reward'), ('bounty program'), ('reward for reporting'),
    ('responsible disclosure'), ('vulnerability report'), ('security vulnerability in your website'),
    ('missing spf'), ('missing dmarc'), ('spf record missing'), ('dmarc record missing'),
    ('clickjacking'),
    -- prize money and lottery scams
    ('prize money'), ('cash prize'), ('claim your prize'), ('you have won'), ('you won'),
    ('lucky winner'), ('lottery'), ('jackpot'), ('winning notification'),
    ('unclaimed funds'), ('inheritance fund'), ('compensation fund'), ('western union'), ('moneygram'),
    -- SEO and marketing pitches
    ('seo'), ('backlink'), ('serp'), ('serps'), ('keyword ranking'), ('keyword rankings'),
    ('search engine optimization'), ('search engine optimisation'),
    ('seo services'), ('seo service'), ('seo expert'), ('seo agency'), ('seo package'), ('seo audit'),
    ('first page of google'), ('page 1 of google'), ('top of google'), ('google first page'),
    ('rank your website'), ('rank higher on google'), ('improve your ranking'), ('improve your rankings'),
    ('search engine ranking'), ('search engine rankings'), ('search engine optimization services'),
    ('guest post'), ('guest posting'), ('sponsored post'), ('domain authority'), ('link building'),
    ('high quality backlinks'), ('website audit'), ('free audit'),
    ('increase your traffic'), ('increase your website traffic'), ('more traffic to your website'),
    ('organic traffic'), ('digital marketing services'), ('digital marketing agency'),
    ('social media marketing services'), ('website redesign'), ('lead generation services')
ON CONFLICT DO NOTHING;

package api

import "testing"

func TestMatchBlockedWord(t *testing.T) {
	words := []string{"crypto investment", "cialis", "Casino"}
	cases := []struct {
		text string
		want string
	}{
		{"Hi, I would like to discuss an Oracle migration.", ""},
		{"I am a specialist in Oracle RAC.", ""},                    // "cialis" inside a word
		{"Best CASINO bonuses here!!!", "Casino"},                   // case-insensitive
		{"Great crypto   investment, act now", "crypto investment"}, // extra spaces
		{"crypto-investment returns", "crypto investment"},          // punctuation between words
		{"buy cialis.", "cialis"},
	}
	for _, c := range cases {
		got, _ := matchBlockedWord(words, c.text)
		if got != c.want {
			t.Errorf("matchBlockedWord(%q) = %q, want %q", c.text, got, c.want)
		}
	}

	// Phrases seeded for bug-bounty begging and prize scams.
	seeded := []string{"ethical hacker", "bug bounty", "missing dmarc", "prize money", "you have won", "lottery", "seo services", "first page of google", "seo"}
	for _, c := range []struct{ text, want string }{
		{"Hi, I am an Ethical Hacker and found a bug on your site.", "ethical hacker"},
		{"Do you offer a BUG-BOUNTY for this?", "bug bounty"},
		{"Your domain has missing DMARC, pay me to explain.", "missing dmarc"},
		{"Congratulations! You have won $1,000,000 prize money.", "prize money"},
		{"We provide SEO-services to put you on the First Page of Google!", "seo services"},
		{"Need SEO? We rank sites fast.", "seo"},
		{"Our geoseo platform", ""}, // whole words only
		{"I'd like your help migrating our Oracle 19c database.", ""},
	} {
		got, _ := matchBlockedWord(seeded, c.text)
		if got != c.want {
			t.Errorf("matchBlockedWord(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

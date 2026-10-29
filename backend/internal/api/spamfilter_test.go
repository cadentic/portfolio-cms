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
}

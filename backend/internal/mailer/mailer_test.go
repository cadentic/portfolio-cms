package mailer

import "testing"

func TestGreetingNamesNeverCarryLinks(t *testing.T) {
	for in, want := range map[string]string{
		"Sayantan Chakraborty": "Sayantan",
		"":                     "there",
		"evil.example":         "there",
		"https://evil.example": "there",
		"win@prize.example":    "there",
		"<b>Hi</b>":            "there",
	} {
		if got := firstName(in); got != want {
			t.Errorf("firstName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"J. Smith":               "J. Smith",
		"Visit www.evil.example": "",
		"see http://x":           "",
	} {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}

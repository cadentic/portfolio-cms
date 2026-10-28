package csvsafe

import (
	"bytes"
	"testing"
)

func TestWriterNeutralisesFormulas(t *testing.T) {
	var b bytes.Buffer
	w := NewWriter(&b)
	_ = w.Write([]string{"=1+1@example.com", "+cmd", "-2", "@SUM(A1)", "plain", ""})
	w.Flush()
	if got, want := b.String(), "'=1+1@example.com,'+cmd,'-2,'@SUM(A1),plain,\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

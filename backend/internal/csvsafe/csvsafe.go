// Package csvsafe writes CSV that spreadsheet apps will not run as formulas.
// Subscriber names, email local parts and provider errors are untrusted, and
// a cell such as =HYPERLINK(...) would execute when the owner opens an export.
package csvsafe

import (
	"encoding/csv"
	"io"
	"strings"
)

// Cell prefixes values that start with a formula trigger with a quote.
func Cell(v string) string {
	if v != "" && strings.ContainsAny(v[:1], "=+-@\t\r") {
		return "'" + v
	}
	return v
}

// Writer is a csv.Writer that neutralises every cell.
type Writer struct{ *csv.Writer }

func NewWriter(w io.Writer) *Writer { return &Writer{csv.NewWriter(w)} }

func (w *Writer) Write(record []string) error {
	out := make([]string, len(record))
	for i, v := range record {
		out[i] = Cell(v)
	}
	return w.Writer.Write(out)
}

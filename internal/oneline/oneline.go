// Package oneline folds a message onto one line, for a warning or a report row that quotes an
// error.
package oneline

import "strings"

// Of folds an error onto a single line and bounds its length.
//
// Because a parser's error is not a sentence. The one this warning was written for arrives
// as eleven lines — pdfcpu prints the page's whole resource inventory after its message —
// and printed raw it takes the warning's shape apart: the continuation lines are
// unindented, so they read as output rather than as the reason for a warning. Taking the
// first line alone would not do either, since the message and the start of the inventory
// share it. Folded and capped, a reader gets the sentence and can re-run for the rest.
func Of(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

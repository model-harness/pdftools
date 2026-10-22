package main

import (
	"strings"
	"testing"
)

// TestChromeReportKeepsItsInlineParts: a report printed from a web page converts with its inline
// code in its sentence and its cell (#21), a wrapped label's words apart (#22), the chart Chrome
// paints outside marked content where it stands (#23), and its ligatures as letters (#24).
func TestChromeReportKeepsItsInlineParts(t *testing.T) {
	md := mdOut(t, fixture(t, "reference/chrome-report.pdf"))
	for _, want := range []string{
		"by its `sailing_id`, and the office files the definitions with the traffic figures.",
		"| Sailing | One departure (`sailing_id`), counted once. |",
		"| Excluded | Test runs and the rows in `EXCLUDED`. |",
		"Monthly passengers on the north route",
		"Sailings that left on time",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("want %q in\n%s", want, md)
		}
	}
	for _, not := range []string{"```", "ﬁ", "ﬃ"} {
		if strings.Contains(md, not) {
			t.Errorf("%q in the output:\n%s", not, md)
		}
	}
	table, image, last := strings.Index(md, "| Excluded"), strings.Index(md, "[image]"), strings.Index(md, "The figures are final.")
	if image < 0 || !(table < image && image < last) {
		t.Errorf("[image] at %d, want it between the table (%d) and the last paragraph (%d):\n%s", image, table, last, md)
	}
}

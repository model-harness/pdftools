package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/objects"
)

// refusingStore is a Store whose second page cannot be opened.
//
// A fake rather than a file, because the only real instance of this is gone: the page that
// used to be refused now loads, so the reporting path it motivated has no input on disk.
// That is the ordinary fate of a fix's own witness, and the reason a fake is worth its
// weight here — probe's job is to say what a file holds and how it will be read, and the
// answer for a page it cannot open is the one thing it must not leave out.
type refusingStore struct{ objects.Store }

func (refusingStore) PageCount() int { return 3 }

func (refusingStore) Page(n int) (objects.Dict, error) {
	if n == 2 {
		return nil, errors.New("missing required resource subdict: Properties\n\tPageResourceNames:\n\tProperties: MC0\n")
	}
	return objects.Dict{
		"Type":     objects.Name("Page"),
		"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
	}, nil
}

func (refusingStore) PageContent(int) ([]byte, error) { return nil, nil }

func (refusingStore) Resolve(o objects.Object) (objects.Object, error) { return o, nil }

// TestProbeReportsAnUnreadablePage pins that probe names a page it could not open.
//
// Without it the per-page table simply skips the row — 1, 3 for a three-page file — while the
// page count still reads 3, which is how a missing page hid in this output for the life of the
// project. The message is folded onto one line for the same reason the md warning is: a
// parser's error is not a sentence, and this one arrives with the page's whole resource
// inventory attached.
func TestProbeReportsAnUnreadablePage(t *testing.T) {
	_, _, _, detail, unreadable := scanPages(refusingStore{}, true)

	if len(unreadable) != 1 {
		t.Fatalf("unreadable = %v, want one entry", unreadable)
	}
	if !strings.Contains(unreadable[0], "page 2") || !strings.Contains(unreadable[0], "Properties") {
		t.Errorf("unreadable[0] = %q, want it to name the page and the reason", unreadable[0])
	}
	if strings.Contains(unreadable[0], "\n") {
		t.Errorf("unreadable[0] spans lines: %q", unreadable[0])
	}
	// The other pages still scan. A refusal costs its own page and nothing else, which is the
	// same policy extract.Document applies one layer up.
	if len(detail) != 2 {
		t.Errorf("%d page rows, want 2 — the two pages that opened", len(detail))
	}
}

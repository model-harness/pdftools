// Package pdfbuild assembles a minimal PDF file from a list of objects, for tests.
//
// It exists for the same reason internal/ttfbuild does, and the reason is the cross-reference
// table. Numbering objects from 1, recording each one's byte offset, and writing an xref whose
// entries are ten-digit offsets in the right order is the only real logic in a hand-built fixture —
// and it is logic a reader has to get right before any test using the file means anything. Copied
// into a second test package it would be the same defect as arithmetic duplicated in two packages:
// a fix to one is invisible to the other, and a subtly wrong offset table fails as "pdfcpu could
// not open this" in a test that is about something else entirely.
//
// Two packages need it: render/native, whose fixtures are streams no producer would emit, and
// cmd/pdfspec, whose fixtures are font dictionaries no producer would emit.
//
// Not in a _test.go file, because an internal package cannot export from one. Internal so it cannot
// become part of the module's surface by accident.
package pdfbuild

import (
	"bytes"
	"fmt"
)

// Bytes returns a one-revision PDF whose objects are numbered 1..len(objs) in the order given.
//
// Object 1 is taken as the catalogue, since /Root has to name something and every caller writes it
// first. Nothing here validates the objects: a fixture's whole purpose is often to be malformed in
// one specific way, and a builder that rejected that would be useless for the tests that need it
// most.
func Bytes(objs []string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	off := make([]int, len(objs))
	for i, o := range objs {
		off[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	start := b.Len()
	// The free entry for object 0 is required and its shape is exact: ten digits, five digits,
	// 'f', and a trailing space. A reader that accepts a short line here accepts nothing else.
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range off {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, start)
	return b.Bytes()
}

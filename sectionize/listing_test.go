package sectionize

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/tag"
)

// TestABreakAlreadyWrittenIsNotWrittenAgain: a span that begins with the line break the extractor
// wrote, as Word's line-end space does when a blank line follows it, gets no second break from
// breakAtBaselines in front of it; one blank line stays one.
func TestABreakAlreadyWrittenIsNotWrittenAgain(t *testing.T) {
	d := docWith(sp{1, 0, "Commit"}, sp{1, 1, "with protection."}, sp{1, 2, "\n\n"}, sp{1, 3, "AI-Tool: Cursor"})
	atLines(d, 10, 700, 688, 664)
	// As Word's: the line-end space and the blank line under it, one span whose box covers both.
	d.Pages[0].Blocks[0].Spans[2].Box.Y0, d.Pages[0].Blocks[0].Spans[2].Box.Y1 = 676, 710
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2, 3))
	out, _ := Tagged(d, tr, DefaultOptions)
	if got, want := out.Sections[0].Blocks[0].Text(), "with protection.\n\nAI-Tool: Cursor"; got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}
}

// TestAMonospacedParagraphIsAListing: a P set wholly in a monospaced face over two lines is code
// on the tagged path, as Word declares a code sample, and one line of it stays a paragraph.
func TestAMonospacedParagraphIsAListing(t *testing.T) {
	d := docWith(sp{1, 0, "Example"}, sp{1, 1, "# one"}, sp{1, 2, "# two"}, sp{1, 3, "x = 1"},
		sp{1, 4, "The records are kept for a year, and the office reads them once a day."})
	atLines(d, 10, 700, 688, 664, 640)
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			for si := range d.Pages[pi].Blocks[bi].Spans {
				if m := d.Pages[pi].Blocks[bi].Spans[si].MCID; m > 0 && m < 4 {
					d.Pages[pi].Blocks[bi].Spans[si].Style.Mono = true
				}
			}
		}
	}
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2), el(tag.RoleP, 1, 3), el(tag.RoleP, 1, 4))
	out, _ := Tagged(d, tr, DefaultOptions)
	blocks := out.Sections[0].Blocks
	if len(blocks) != 3 || blocks[0].Role != doc.RoleCode || blocks[0].Text() != "# one\n# two" || blocks[1].Role != doc.RoleParagraph {
		var got []string
		for _, b := range blocks {
			got = append(got, string(b.Role)+":"+b.Text())
		}
		t.Errorf("blocks %q, want a listing of two lines, then two paragraphs", got)
	}
}

// TestBreaksOutsideAListingFold: on the tagged path, one line of code whose span ends in the break
// the extractor wrote, because the next line went to another element, is a paragraph with no
// break in it; and a break that ends a listing's line is not written twice.
func TestBreaksOutsideAListingFold(t *testing.T) {
	d := docWith(sp{1, 0, "Example"}, sp{1, 1, "(This is a string)\n"}, sp{1, 2, "(So is this one)"},
		sp{1, 3, "The records are kept for a year, and the office reads them once a day."})
	atLines(d, 10, 700, 688, 664)
	for si := range d.Pages[0].Blocks[0].Spans {
		if m := d.Pages[0].Blocks[0].Spans[si].MCID; m == 1 || m == 2 {
			d.Pages[0].Blocks[0].Spans[si].Style.Mono = true
		}
	}
	out, _ := Tagged(d, tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1), el(tag.RoleP, 1, 2), el(tag.RoleP, 1, 3)), DefaultOptions)
	if b := out.Sections[0].Blocks[0]; b.Role != doc.RoleParagraph || b.Text() != "(This is a string) " {
		t.Errorf("%s %q, want a paragraph with its break folded", b.Role, b.Text())
	}

	d = docWith(sp{1, 0, "Example"}, sp{1, 1, "endobj\n"}, sp{1, 2, "11 0 obj"})
	atLines(d, 10, 700, 688)
	out, _ = Tagged(d, tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2)), DefaultOptions)
	if got, want := out.Sections[0].Blocks[0].Text(), "endobj\n11 0 obj"; got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}
}

// TestAMonospacedCellStaysACell: only a P becomes a listing; a table cell whose text is set in a
// monospaced face over two lines stays in its table.
func TestAMonospacedCellStaysACell(t *testing.T) {
	d := docWith(sp{1, 0, "Commands"}, sp{1, 1, "blame"}, sp{1, 2, "--porcelain"},
		sp{1, 3, "The records are kept for a year, and the office reads them once a day."})
	atLines(d, 10, 700, 688, 664)
	for si := range d.Pages[0].Blocks[0].Spans {
		if m := d.Pages[0].Blocks[0].Spans[si].MCID; m == 1 || m == 2 {
			d.Pages[0].Blocks[0].Spans[si].Style.Mono = true
		}
	}
	tbl := kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 1, 2)))
	out, _ := Tagged(d, tree(el(tag.RoleH2, 1, 0), tbl, el(tag.RoleP, 1, 3)), DefaultOptions)
	if b := out.Sections[0].Blocks[0]; b.Role != doc.RoleTableCell {
		t.Errorf("the cell is %s %q, want a table cell", b.Role, b.Text())
	}
}

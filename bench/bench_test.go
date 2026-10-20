package bench

import (
	"math"
	"testing"
)

// close reports whether got is within tol of want, which is what every float
// assertion here uses: the metrics are ratios over small, hand-built documents, and
// pinning them to an exact float invites a test that breaks on an unrelated rounding
// change rather than on the behaviour it names.
func close(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v ± %v", name, got, want, tol)
	}
}

func TestScoreIdenticalInputIsPerfect(t *testing.T) {
	md := []byte(`# Title

A paragraph with **bold**, *italic*, ` + "`code`" + `, and ~~strike~~ text.

- First item
- Second item

| A | B |
| --- | --- |
| 1 | 2 |
`)
	s := Score(md, md)
	close(t, "CER", s.CER, 0, 0)
	close(t, "Recall", s.Recall, 1, 0)
	close(t, "Precision", s.Precision, 1, 0)
	close(t, "Order", s.Order, 1, 0)
	close(t, "Blocks", s.Blocks, 1, 0)
	close(t, "Headings", s.Headings, 1, 0)
	close(t, "Tables", s.Tables, 1, 0)
	close(t, "Style", s.Style, 1, 0)
	if s.RefHeadings != 1 {
		t.Errorf("RefHeadings = %d, want 1", s.RefHeadings)
	}
	if s.RefCells != 4 {
		t.Errorf("RefCells = %d, want 4", s.RefCells)
	}
	if s.RefSpans != 4 {
		t.Errorf("RefSpans = %d, want 4", s.RefSpans)
	}
}

// Reordering content is the one change that must cost Order and nothing structural:
// matching is by content, not position, so every reference block still finds its own
// text verbatim and CER stays 0.
func TestScoreSwappedParagraphsLowerOrderOnly(t *testing.T) {
	ref := []byte(`# Title

First paragraph stays in front of the second one in the reference.

Second paragraph follows the first one in the reference.
`)
	got := []byte(`# Title

Second paragraph follows the first one in the reference.

First paragraph stays in front of the second one in the reference.
`)
	s := Score(ref, got)
	close(t, "CER", s.CER, 0, 0)
	if s.Order >= 1 {
		t.Errorf("Order = %v, want < 1", s.Order)
	}
	close(t, "Blocks", s.Blocks, 1, 0)
	close(t, "Recall", s.Recall, 1, 0)
	close(t, "Precision", s.Precision, 1, 0)
	close(t, "Headings", s.Headings, 1, 0)
}

// Demoting every heading by one level must cost Headings and nothing else: the block
// text is unchanged, so it still matches and still counts as intact.
func TestScoreDemotedHeadingsLowerHeadingsOnly(t *testing.T) {
	ref := []byte(`# Top level section

Some body text under the top heading.

## Second level section

More body text under the second heading.
`)
	got := []byte(`## Top level section

Some body text under the top heading.

### Second level section

More body text under the second heading.
`)
	s := Score(ref, got)
	if s.Headings >= 1 {
		t.Errorf("Headings = %v, want < 1", s.Headings)
	}
	close(t, "CER", s.CER, 0, 0)
	close(t, "Order", s.Order, 1, 0)
	close(t, "Blocks", s.Blocks, 1, 0)
}

// A single changed cell must cost Tables, nudge CER, and leave the table itself
// matched and intact — the structural dimensions, Blocks and Order, don't move.
func TestScoreChangedTableCellLowersTablesAndCER(t *testing.T) {
	ref := []byte(`| Name | City | Role |
| --- | --- | --- |
| Alice | Boston | Engineer |
| Bob | Chicago | Designer |
`)
	got := []byte(`| Name | City | Role |
| --- | --- | --- |
| Alice | Bostan | Engineer |
| Bob | Chicago | Designer |
`)
	s := Score(ref, got)
	if s.Tables >= 1 {
		t.Errorf("Tables = %v, want < 1", s.Tables)
	}
	if s.CER <= 0 {
		t.Errorf("CER = %v, want > 0", s.CER)
	}
	close(t, "Blocks", s.Blocks, 1, 0)
	close(t, "Order", s.Order, 1, 0)
}

// Dropping the emphasis markers around two words changes nothing about the text
// goldmark hands back for the paragraph — the asterisks are syntax, not content — so
// only Style should move.
func TestScoreDroppedEmphasisLowersStyleOnly(t *testing.T) {
	ref := []byte("This sentence has **bold** and *italic* words in it for testing.\n")
	got := []byte("This sentence has bold and italic words in it for testing.\n")
	s := Score(ref, got)
	if s.Style >= 1 {
		t.Errorf("Style = %v, want < 1", s.Style)
	}
	close(t, "CER", s.CER, 0, 0)
	close(t, "Blocks", s.Blocks, 1, 0)
	close(t, "Order", s.Order, 1, 0)
}

// A single-character typo must still match and still count as intact for a list item
// long enough that one edit doesn't cross the 0.9 similarity floor, but it is still an
// edit and CER must show it.
func TestScoreTypoInListItemRaisesCERButKeepsBlocks(t *testing.T) {
	ref := []byte("- Buy milk at the store today\n- Walk the dog around the block\n")
	got := []byte("- Buy milk at the store todiy\n- Walk the dog around the block\n")
	s := Score(ref, got)
	if s.CER <= 0 {
		t.Errorf("CER = %v, want > 0", s.CER)
	}
	close(t, "Blocks", s.Blocks, 1, 0)
}

// Fusing four reference paragraphs into one output block leaves at most one of the
// four with a partner, so Blocks must drop. Every character is still there and in
// order, so CER must stay 0: the merge is a structure defect, not a text one.
func TestScoreMergedParagraphsLowerBlocks(t *testing.T) {
	ref := []byte(`Alpha reports one result from the first run.

Bravo reports two results from the second run.

Charlie reports three results from the third run.

Delta reports four results from the fourth run.
`)
	got := []byte("Alpha reports one result from the first run. Bravo reports two results from the second run. Charlie reports three results from the third run. Delta reports four results from the fourth run.\n")
	s := Score(ref, got)
	if s.Blocks >= 1 {
		t.Errorf("Blocks = %v, want < 1", s.Blocks)
	}
	close(t, "CER", s.CER, 0, 0)
}

// A paragraph split in two, as a column break splits it, is the merge turned round:
// Blocks drops and CER stays 0, because the second half follows the first back into
// place even though only the first half has a partner.
func TestScoreSplitParagraphLowersBlocksOnly(t *testing.T) {
	ref := []byte("The second paragraph begins at the foot of the left column and ends at the top of the right one, after the break.\n")
	got := []byte("The second paragraph begins at the foot of the left column and ends\n\nat the top of the right one, after the break.\n")
	s := Score(ref, got)
	if s.Blocks >= 1 {
		t.Errorf("Blocks = %v, want < 1", s.Blocks)
	}
	close(t, "CER", s.CER, 0, 0)
	close(t, "Order", s.Order, 1, 0)
}

func TestScoreErrAndImageMarkersAreCounted(t *testing.T) {
	ref := []byte("A page of plain text.\n")
	got := []byte("[err] A page that failed, with an [image] in it, and another [err] below, plus a second [image].\n")
	s := Score(ref, got)
	if s.Errs != 2 {
		t.Errorf("Errs = %d, want 2", s.Errs)
	}
	if s.Images != 2 {
		t.Errorf("Images = %d, want 2", s.Images)
	}
}

// Empty input is a real case, not an error: a page that extracted to nothing is valid
// gold, and a converter that produced nothing is a legitimate (bad) result to score.
// Neither direction may divide by zero.
func TestScoreEmptyInputDoesNotPanicOrNaN(t *testing.T) {
	cases := map[string][2][]byte{
		"both empty": {[]byte(""), []byte("")},
		"ref empty":  {[]byte(""), []byte("Some output text.\n")},
		"got empty":  {[]byte("Some reference text.\n"), []byte("")},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			s := Score(in[0], in[1])
			for field, v := range map[string]float64{
				"CER": s.CER, "Recall": s.Recall, "Precision": s.Precision,
				"Order": s.Order, "Blocks": s.Blocks, "Headings": s.Headings,
				"Tables": s.Tables, "Style": s.Style,
			} {
				if math.IsNaN(v) {
					t.Errorf("%s is NaN", field)
				}
			}
		})
	}
}

// A pair matches at a similarity of 0.5 and not below it. Two blocks swap places and
// each is half rewritten, so they match and Order sees the swap; rewritten by 60% they
// match nothing and there is no order to judge.
func TestScoreMatchesAtHalfSimilarity(t *testing.T) {
	ref := []byte("abcdefghij\n\nklmnopqrst\n")
	half := Score(ref, []byte("klmnoABCDE\n\nabcdeVWXYZ\n"))
	close(t, "Order at 0.5", half.Order, 0, 0)
	less := Score(ref, []byte("klmnABCDEF\n\nabcdUVWXYZ\n"))
	close(t, "Order at 0.4", less.Order, 1, 0)
}

// A matched block is intact at a similarity of 0.9 and not below: one changed character
// in ten keeps Blocks at 1, and two make it 0.
func TestScoreBlockIsIntactAtNineTenths(t *testing.T) {
	ref := []byte("abcdefghij\n")
	close(t, "Blocks at 0.9", Score(ref, []byte("abcdefghiZ\n")).Blocks, 1, 0)
	close(t, "Blocks at 0.8", Score(ref, []byte("abcdefghYZ\n")).Blocks, 0, 0)
}

// Matching compares characters among the three output blocks closest in words, not only
// the closest one. The same words reordered are closest in words and far in characters;
// the reference with two letters wrong is the reverse, and is the block that matches.
func TestScoreMatchLooksPastTheClosestWordBag(t *testing.T) {
	ref := []byte("red green blue yellow\n")
	got := []byte("blue yellow red green\n\nred grean blue yelow\n")
	s := Score(ref, got)
	close(t, "Blocks", s.Blocks, 2.0/3, 1e-9)
}

// A word whose case changed is a different word to Recall and Precision, and the same
// block to matching. So Blocks stays 1, and CER counts the three letters.
func TestScoreCaseCountsForWordsNotForBlocks(t *testing.T) {
	s := Score([]byte("The Harbour Report\n"), []byte("the harbour report\n"))
	close(t, "Recall", s.Recall, 0, 0)
	close(t, "Precision", s.Precision, 0, 0)
	close(t, "Blocks", s.Blocks, 1, 0)
	close(t, "CER", s.CER, 3.0/16, 1e-9)
}

// A paragraph wrapped over two source lines is the same paragraph on one: the line break
// is a space, and does not join the words either side of it.
func TestScoreSoftLineBreakIsASpace(t *testing.T) {
	s := Score([]byte("alpha beta\ngamma delta\n"), []byte("alpha beta gamma delta\n"))
	close(t, "Recall", s.Recall, 1, 0)
	close(t, "Precision", s.Precision, 1, 0)
}

// Two identical paragraphs each match their own output block, never the same one twice,
// so a document that repeats a line scores as the perfect copy it is.
func TestScoreRepeatedParagraphsMatchOnce(t *testing.T) {
	md := []byte("Same line.\n\nSame line.\n\nAnother line.\n")
	s := Score(md, md)
	close(t, "Order", s.Order, 1, 0)
	close(t, "Blocks", s.Blocks, 1, 0)
}

// Bold inside a tight list item and inside a table cell is style like any other: a
// converter that drops it loses on Style.
func TestScoreStyleInListItemsAndTableCells(t *testing.T) {
	ref := []byte("- one **bold** item\n- another\n\n| **Head** | B |\n| --- | --- |\n| x | y |\n")
	got := []byte("- one bold item\n- another\n\n| Head | B |\n| --- | --- |\n| x | y |\n")
	s := Score(ref, got)
	if s.RefSpans != 2 {
		t.Errorf("RefSpans = %d, want 2: the item's bold and the cell's", s.RefSpans)
	}
	close(t, "Style", s.Style, 0, 0)
}

// A table that came out as paragraphs keeps its words and loses its grid. It must not
// count as the table: Tables is 0, and Blocks is below 1 because nothing intact is a
// table.
func TestScoreTableAsParagraphsIsNotTheTable(t *testing.T) {
	ref := []byte("| Month | Visitors |\n| --- | --- |\n| May | 3920 |\n")
	got := []byte("Month Visitors May 3920\n")
	s := Score(ref, got)
	close(t, "Tables", s.Tables, 0, 0)
	close(t, "Blocks", s.Blocks, 0, 0)
	close(t, "CER", s.CER, 0, 0)
}

// An output table too unlike the reference's to match earns no credit for the cells it
// happens to share, here the header: a cell counts only inside the table it matched.
func TestScoreUnmatchedTableEarnsNoCells(t *testing.T) {
	ref := []byte("| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |\n| 5 | 6 |\n| 7 | 8 |\n")
	got := []byte("| A | B |\n| --- | --- |\n| x | y |\n")
	close(t, "Tables", Score(ref, got).Tables, 0, 0)
}

// An [err] is one character the converter could not read and is scored as one: one edit in
// CER, and no word in the bag (ADR 0042).
func TestScoreErrIsOneUnreadCharacter(t *testing.T) {
	s := Score([]byte("abcde fgh\n"), []byte("ab[err]de fgh\n"))
	if want := 1.0 / 8; math.Abs(s.CER-want) > 1e-9 {
		t.Errorf("CER = %.4f, want %.4f", s.CER, want)
	}
	if p := Score([]byte("abc fgh\n"), []byte("abc [err] fgh\n")).Precision; p != 1 {
		t.Errorf("Precision = %.4f, want 1: err is not a word the page has", p)
	}
	if s.Errs != 1 {
		t.Errorf("Errs = %d, want 1", s.Errs)
	}
}

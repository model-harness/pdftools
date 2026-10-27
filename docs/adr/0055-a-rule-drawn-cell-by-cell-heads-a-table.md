# 55. A rule drawn cell by cell heads a table

Date: 2026-10-27

## Status

Accepted.

## Context

The 26-page Word paper of ADR 0054 has six tables. Every one converted as a run of one-line paragraphs, a cell to each. It was read locally only, since it is not ours to commit. Its contact sheets show what the page draws: each table is ruled only under its header row. The rows are wrapped text in narrow columns, with no rule between them and none down the side.

Three things lost the tables, and one more lost words inside them.

- **Word declares these tables as bare `P`s,** one per cell, in row order. There is no `Table`, `TR` or `TD` to read, so the tagged path has nothing to call a table, and the untagged path never runs on a tagged document.
- **The rule under the header is the only evidence of the columns.** Word draws it as each header cell's own bottom border: a segment per cell, with a quarter-point square where two meet. On page 19 the square starts inside the border before it.
- **A table Word did tag can carry no `TH`.** The first row is then a header the sink cannot see, and it writes an empty header row above it.
- **Word breaks a word that is too wide for its cell by itself,** and draws no space or hyphen at the break: "Confidenc" over "e", "Recommende" over "d". Everywhere else on such a page, Word draws the space it wraps a line at. So the extractor's inferred wrap space split the word, and the paper read "Confidenc e".

## Decision

**A rule drawn cell by cell names a table's columns** (`layout.RuledHeaders`). Horizontal rules at one height, within a point of each other, are one rule: a thin rectangle's two long edges. Within it:

- Abutting segments with a joint of a point or less between each, two or more borders long, are a header. Each border is a column.
- A joint may start inside the border before it by up to its own width.
- A rule drawn whole is prose's, a page's or a caption's, and is no header.
- Two segments that touch with no joint are one rule drawn in two strokes, and are no header.
- Segments that overlap, as a shaded box's lines do, are no borders at all.

**Bare paragraphs over and under it are that table** (`sectionize` `ruledTables`, after unclaimed content is placed).

- **The header** is two or more paragraphs, each in its own column, left to right, whose last line sits on the rule.
- **The rows** are the paragraphs below it in one of its columns. A new row starts wherever a cell's column does not advance past the one before it. A row's cells come in order, and a row's first cell is left of its last.
- **The table ends** where a row starts more than 1.5 times as far below the row above as the first row sits below the header. A cell's padding sets the pitch, and the paragraph after a table sits further off. Without it, one of the paper's tables took in the paragraph after it.
- **It runs on across a page** where the next page draws a rule with the same columns. Word repeats the header row there, in one of two ways, and both are dropped:
  - as more marked content of the same header `P`s;
  - as blocks of their own that read the same.
- **A body cell may run onto the next page** in the same column under that repeated rule. That is a row Word broke across the page break.
- **A next page with other header names, or a rule with other columns,** starts a table of its own.
- **Its number** comes after every table the outline already declares.

**A table Word tagged with no `TH`** has its first row marked as the header (`markHeaders`) when all of these hold:

- the page rules that row cell by cell;
- the row is two or more cells, sitting on the rule;
- no row below it is ruled the same way, since a grid rules every row alike and says nothing about the first.

**A wrap after a line of one word, with no space drawn, may break the word** (`extract`). Two conditions make it a candidate:

- **The page's habit** (`drawsWrapSpaces`). The page must draw the space at the lines it wraps between words: five or more such wraps draw it, and more draw it than do not. A wrap at a dash, before a capital, or between lines set in other directions is no wrap between words. LaTeX and Chrome draw no wrap spaces, so their pages are untouched.
- **The line is one word** (`oneWordLine`), and a letter meets a lowercase letter at the break. Word breaks a word only when it is too wide for the line by itself. A wrap after a line of several words is a space even where none is drawn, as in the Adobe sample's "Thomas Booker" / "speaking".

Such a wrap is written as U+FDD0, a noncharacter no text holds. `resolveBreaks` decides each mark before a page or a document leaves the package, by the document's own words. This is the evidence `layout.Hyphens` takes a hyphen by:

- the parts join when the joined word is one the document uses elsewhere;
- they also join when the second part is no word it uses;
- otherwise the mark becomes the space.

So "Confidenc" and "e" join, and ISO 32000-2's narrow header cell "Object" / "number" keeps its space. `Page` decides by the page's words, and `Document` by every page's.

## Acceptance

- **The paper:** all six tables convert as tables, with their headers, wrapped cells, and rows run on across page breaks.
  - The tables are on pages 13–14, 16, 17, 19–20, 21–22 and 26: 49 Markdown table rows in all.
  - The 12 repeated header words are dropped.
  - "Confidence" and "Recommended" are whole.
  - The three code samples of ADR 0054 are unchanged.
- **A Word fixture of our own, `testdata/reference/word-report`.** It is built by `word-report.ps1` and signed `pdftools` rather than with the name of whoever runs Word. It holds:
  - a wrapped paragraph;
  - a table ruled only under its header, whose narrow columns make Word break "Confidence" and "Recommended";
  - the code sample from ADR 0054.

  It scores 1.000 on every dimension, and that is its floor. The build before this change wrote its header as an empty row and its words split.
  - Word tags this table, so the fixture exercises header marking and word breaks. The bare-`P` path is exercised by unit tests and by the paper.
- **Old against new** over the 76 PDFs here: only the paper and the new fixture change.
  - An earlier version decided every no-space wrap on the page by vocabulary alone. It joined Adobe's "Booker speaking", ISO 32000-2's ColorDodge "𝑐𝑠 𝑐𝑏" and an ISO 19593 line, and the one-word-line rule is what removed them.
  - ISO 32000-2's "AbsoluteColorimetri c", a wrap after a line of several words, stays split.
- **`TestNoWordMarkLeavesTheExtractor`** extracts every committed fixture and every corpus document present, and finds no mark.

## Mutation

68 mutants cover `drawsWrapSpaces`, `oneWordLine`, `brokenWord`, the call site, `resolveBreaks`, `RuledHeaders`, `ruledTables` and `markHeaders`, plus the wiring of each. 67 are killed, each by a named test. The 68th, without `nextPage <= page`, never finishes: that guard is what ends the loop over pages.

- **Five guards could not change the output, and were deleted rather than tested:**
  - `oneWordLine`'s empty-lines check and its offset clamp: a block a line runs on into has recorded its lines, each within its spans;
  - the habit's check for an empty line end;
  - the vocabulary's exclusion of the word before a mark, which is never asked about;
  - a ruled table's test of the next page's first block, which the next pass over that block makes anyway.
- **The first fixtures tested only the sign of each rule, and a case was added for each boundary or condition:**
  - the habit at exactly four and five, and at as many drawn as not;
  - a wrap at a hyphen, before a capital, and from a line set sideways;
  - an empty last fragment, and a line starting with a space;
  - a one-word line before a capital or a figure, and a figure before a word;
  - line starts in a later span, and a second part in the next span;
  - a vertical rule where a joint would be, a mark inside a border, a segment overlapping two cells, and two tables side by side on one rule;
  - a header of one cell, a rule with none over it, a caption well above it, and a header column taken twice;
  - a row with an empty cell, and a paragraph 1.75 times the pitch below the table;
  - a next page with other header names, a moved column or a column fewer, and a next page whose rule sits lower;
  - a cell running on two pages, above the next page's rule, or into another column;
  - a declared table numbered before a ruled one;
  - a declared first row of one cell, with a `TH`, or far from the rule.
- 15 mutants did not compile, and were rewritten so that they did.

## Consequences

- A Word table whose structure was lost in export converts as the table the page draws, wherever its header is ruled cell by cell. A table ruled whole, or not at all, and an untagged one with wrapped cells, are still read only as before.
- A Word page's words broken in narrow cells are whole again. A page from a producer that draws no wrap spaces, and a wrap after a line of several words, are decided as before.
- A tagged table with no `TH` under a ruled first row gains its header.

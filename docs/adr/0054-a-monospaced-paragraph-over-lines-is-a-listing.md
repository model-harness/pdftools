# 54. A monospaced paragraph over lines is a listing

Date: 2026-10-26

## Status

Accepted.

## Context

A 26-page paper exported by Word converted its three code samples as one line of inline code each: "`# @ai-annotation` `# AI-Tool: Cursor 0.45` …". It was read locally only, since it is not ours to commit. The page sets each sample as a shaded box in Consolas, its lines broken, indented and separated by blank lines.

Two things lost the lines, and neither was the sample's text.

- **Word declares a code sample a `P`.** It is one paragraph whose lines its author broke by hand, with Shift+Enter, and its marked content runs across the lines: one span holds "`# AI-Tool: Cursor 0.45 # AI-Model: …`". A `P` is prose to every sink, which folds a line end to a space.
- **The extractor joined the lines the way it joins a wrap.** `appendLine` writes a space at a line end unless one is drawn there, and Word draws one. So the line ends were gone by the time any role was decided. `breakAtBaselines`, which restores a declared `Code`'s lines, breaks only between two spans, and here one span held them all.

The same loss was in the corpus, unreported. ISO 32000-2, ISO/TS 32003, 32004 and 32005 and pymupdf's v110-changes set PDF objects, XML, C headers and changelog entries as monospaced paragraphs, and each came out as one line of inline code.

## Decision

**The extractor keeps a listing's line ends.** Where two lines of one block are set wholly in a monospaced face, `appendLine` joins them with a line break, not with a wrap's space:

- The space a producer drew at the line's end goes.
- The next line's leading spaces stay: they are its indentation.
- A line of white space alone, as Word draws an empty line of a code sample, continues a listing. `monoTail` looks back past it to the last line with ink.

A line in a proportional face on either side is a wrap, as before, so a file name at the end of a sentence stays in the sentence.

**A listing is a face, not a role** (`layout.Listing`). Spans set wholly in a monospaced face, over two or more lines with ink, are a listing.

- One line is code in a sentence, and stays inline. That includes ISO 32000-2's "12 0 R", whose span ran on into the blank line under it.
- A proportional span among them is prose quoting code.
- A document whose body is mostly monospaced, a typescript or a plain-text page, says nothing by its face. So `layout.MonoBody`, a strict majority of the document's text, turns the rule off.

Both paths ask that one function:

- **Untagged:** `layout.Listings`, the first pass `InferRoles` runs after furniture and before tables, makes such a paragraph `RoleCode`.
- **Tagged:** sectionize makes a `P` that is one `RoleCode` before its spans are joined. So `spaceAtGaps` leaves it alone and `breakAtBaselines` writes the line ends the spans carry.

This overrides a declaration only where the declaration cannot be meant: a `P` is prose, and these are not. The role is the producer's word, and the face is its drawing.

**Everywhere else, a break is the wrap's space** (`layout.FoldBreaks`). The extractor joins two monospaced lines before it knows which element each belongs to. ISO 32000-2 tags each line of an example as its own `P`, so one line's span ended in a break the next line's element took the rest from, and the first version of this change wrote `` `(This is a string)\n` `` into 4,417 lines. Any block that is not a listing has its breaks written back as spaces, which restores that output exactly.

**Two smaller fixes the listings needed:**

- `breakAtBaselines` writes no second break where one is already written: at the end of the line, as the extractor writes it, or at the start of the next span. Word draws a line's end-space in Calibri, as its own span, and when a blank line follows, the extractor's break lands in that span. Its box covers both lines, so `breakAtBaselines` read it as a line below and broke again, making one blank line two.
- The sink trims a listing's trailing white space, not only its trailing line ends. "endobj" followed by a line end and a drawn space wrote an empty last line inside the fence.

## Acceptance

- **The paper:** all three samples are fenced exactly as the page sets them: the Python comment block with its indented last line, the commit trailer with its two blank lines, and the JSON with its nesting, one block across the page break. Nothing else in the 26 pages changed.
- **A Word fixture of our own** reproduces Word's way of setting a sample: one shaded Consolas paragraph, broken by hand, with an indented line and a blank one. It found the blank-line case, which the paper's samples did not exercise, and it converts as its source says. It is held back to land with the table work, since its table also exercises what that work fixes, and it would fail the gold set's table target today.
- **Old against new** over the 74 PDFs here: five documents change, all by new listings and nothing else.
  - ISO 32000-2 gains 40, all PDF syntax examples.
  - ISO/TS 32004 gains 7, ISO/TS 32003 2, ISO/TS 32005 1 (its XML declaration), and pymupdf's v110-changes 3 (a C header among them).

  Each was one line of inline code before. A line the typesetter wrapped stays wrapped, as the page shows it. An example its tree splits into two `P`s is two fences, as it was two paragraphs.
- **`TestListingIndentsReachTheirListing`'s figure goes from 3,944 to 4,119,** and the 175 reconcile by document:
  - ISO 32000-2 +59: its new listings' 83 indented lines, less 24 that were one-line paragraphs and are now lines of them;
  - ISO/TS 32004 +106, 108 less 2;
  - ISO/TS 32005 +10.

  No other document moved.

## Mutation

25 mutants, one for each condition of the extractor's join, `Listing`, `MonoBody`, the tagged path's role and fold, both `breakAtBaselines` checks and the sink's trim, are all killed, each by a named test.

- **Two guards were redundant, and were deleted rather than tested.** Both of their mutants survived because they could not change the output:
  - the trim's offset: spaces and tabs cannot reach back past a line's start;
  - a `!listing` before `WrapSpace`: a listing's break is white space, after which `WrapSpace` adds nothing.
- **Six guards had no case that could tell them apart, and now have one:**
  - a fraction set in Courier, for `joinPrev`;
  - the joining line's end-space drawn in another face, as Word draws it;
  - a line of prose and code before a listing line;
  - a listing across a page break;
  - line boxes that touch;
  - a monospaced table cell, which must stay in its table.
- Three mutants did not compile, and were rewritten so that they did.

## Consequences

- A code sample set in a monospaced face converts as a listing, from Word, from LaTeX's verbatim, or from a specification's examples, whatever role its tree gives it.
- A monospaced paragraph that is one line stays inline code, and a whole typescript stays prose.
- A table cell or list item whose text is monospaced over lines keeps its role. Only a `P` becomes a listing.

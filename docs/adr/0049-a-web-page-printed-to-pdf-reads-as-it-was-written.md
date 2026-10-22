# 49. A web page printed to PDF reads as it was written

Date: 2026-10-22

## Status

Accepted. Closes issues #21, #22, #23 and #24.

## Context

A 14-page analytics report, printed from a web page by Chrome, converted with v1.0.0 with no `[err]` and exit 0, and still read wrongly in four places:

1. **Inline code left its sentence (#21).** In a table of definitions, "One skills-manager installation (`install_id`)" came out as "( )", with `install_id` fenced as a code block after the table.
2. **A wrapped label lost its space (#22).** "Monthly engaged installs" wrapping onto "(MAU)" came out as "installs(MAU)", along with "incl.background" and "baseactive".
3. **A chart's image was dropped (#23).** Gemma described the heatmap on page 4, but the output had no line for it.
4. **Ligatures stayed ligatures (#24).** "Deﬁnitions" and "traﬃc" kept U+FB01 and U+FB03. ADR 0029 had noted XeTeX's "ff" as U+FB00 for #8, and it was never done.

The untagged path (`md -flat`) read the first three correctly, so the defects were in how the tagged path rebuilds text from Chrome's structure tree. Chrome tags `<code>` as a Code element between the runs of its sentence. It tags each card's label as one marked-content sequence over two lines, which its wide leading made the extractor read as two blocks. And it paints an SVG `<image>` outside marked content altogether.

## Decision

**A Code that shares its parent with other text is inline** (`inlineCode`). ISO 32000 counts Code among the inline-level elements. `gather` detached every Code kid as a block. It now absorbs a Code whose parent holds anything else, either its own content or another kid, and the sink writes its monospace text as inline code. A Code that is its parent's whole content is still a listing and is still fenced.

**A wrap the extractor did not make gets the wrap's space** (`wrapJoin`). When an element's text runs from one line to a lower one, `spaceAtGaps` asks `extract.WrapSpace`, the extractor's own wrap rule, whether a space joins them. The join must meet three conditions:

- the next span's top is at or below the previous span's bottom, measured top to bottom because a span's box covers all its lines;
- the next span returns to the start of the line, so a stacked fraction's denominator, centred under its numerator, is not a wrap;
- the previous span does not end in `/`, which extract's fraction rule writes and after which a path wraps.

It is not applied across a page, as `TestNoSpaceIsInferredAcrossAPageBoundary` already settles. Nor is it applied between two spans of one extractor block, since the extractor already joined those and decided whether a space belongs. That second condition is what keeps ISO/TS 32005's "exposi-" and "tion" one word, its hyphen being marked content that layout.Hyphens removes. The copy `substituted` makes for an element's `/ActualText`, such as a declared soft hyphen, belongs to the block of the spans it stands for.

**An image no element claims is placed where it is painted** (`placeUnclaimed`). Every image `layout.Keep` keeps that the tree did not claim goes among the outline's blocks on its page, at the slot `layout.ImageSlot` gives. That function is now the untagged path's placement rule, so the two paths put one image in one place.

- An image is never placed inside a table's cells; it goes before or after the table.
- An image that mostly overlaps one the tree claimed is the same picture painted twice, and is left out. ISO 32000-2's Figure 48 paints its photograph at 1444 by 964 samples, unmarked, under the 340 by 258 image its Figure claims.
- A page the tree built nothing for gives its images to Unplaced, as it gives its unclaimed text.

**A ligature a letter touches is spelled as its letters** (`spellLigatures`). The seven Latin presentation forms, U+FB00 to U+FB06, become their compatibility decompositions in every span the extractor builds. Line offsets move with the bytes. A ligature standing alone is the character itself: ISO 32000-2's table of glyph names sets "ﬀ" beside "ff", and would say nothing if both read "ff". NFKC over the whole text is not used, since it would also turn a superscript two into a two.

## Acceptance

**New fixture `chrome-report`,** a small report printed by Chrome. It holds inline code in a sentence and in a table cell, card labels that wrap with wide leading, a chart drawn as an SVG `<image>`, and two ligature code points. It scores 1.000 on every dimension against its hand-written gold. `TestChromeReportKeepsItsInlineParts` checks each of the four defects.

**The report itself** now reads "Monthly engaged installs (MAU)", "any signal, incl. background beacon" and "share of monthly base active", with `install_id` and `EXCLUDED_INSTALLS` in their cells. It has no code fences. "Definitions" and "traffic" are spelled out. Gemma's description of the heatmap stands where the heatmap is.

**The gold set:** xelatex-article goes from CER 0.008 to 0.000, and from recall, precision and blocks of 0.979, 0.979 and 0.857 to 1.000, because its "Traffic" heading reads as letters.

**The corpus,** converted with v1.0.0 and with this change and compared line by line, changes in 11 documents:

- ISO 32000-2 and four ISO technical specifications: sentences, notes and equations that ran together are separated ("content.NOTE 1" becomes "content. NOTE 1"), and "dictionaryKey Type ValueC0" becomes "dictionary** **Key Type Value** **C0".
- ISO 32000-2 gains four images its tree did not claim: two 3D artwork views and two EBNF diagrams.
- 2201.00069, autotagPDFInput, text-find-ligatures and xelatex-article: ligatures are spelled ("identified", "offset", "flux", "files", "flag", "Traffic").
- WTPDF: entries that ran together are separated ("footnote*Endnote*" becomes "footnote *Endnote*").
- sampleInvoice: five page images `layout.Keep` keeps are now written, as the untagged path writes them.

Four regressions surfaced along the way, and each was fixed before acceptance:

- The wrap space split hyphenated words in four ISO documents, such as "exposi tion", "di gest" and "algo rithm". The same-block condition fixed it.
- It wrote "/ 2" in ISO 32000-2's fractions. The line-start and slash conditions fixed it.
- Spelling every ligature emptied ISO 32000-2's glyph table of its point. Spelling only inside words fixed it.
- Figure 48 gained a second image line. The overlap test fixed it.

**Tests.**

- `TestCodeInsideTextIsInline` covers a Code that owns its text, one held in kids, and a listing.
- `TestAWrapTheExtractorDidNotJoinIsASpace` covers two blocks against one, the line start, the drop and the slash.
- `TestASubstitutedSpanJoinsAsItsText` covers a declared soft hyphen.
- `TestAnUnclaimedImageStandsWhereItIsPainted` covers between paragraphs, before a table, after a table that runs on to the next page, a decoration, a doubled picture and a page with no blocks.
- `TestLigaturesAreSpelledInsideWords` covers inside words, standing alone, and line offsets.
- `TestMDOutlineConservesText` now counts a bare `[image]` as markup.

## Mutation

The 30 mutants were all killed, each by a named test. They cover the inline-code rule, every condition of the wrap rule, the substitute's block, the placement and overlap of unclaimed images, the ligature spelling and its offsets, and `ImageSlot`.

The call that spells a page's spans in `extract.Page` is held by `TestChromeReportKeepsItsInlineParts`, in `cmd/pdfspec`.

**7 survived the first batch,** and 6 did not build and were rewritten so they did.

- **Three guards on the wrap rule were masked:** the page guard, the guard against spans with no box, and the exclusion of code listings. Every older test builds a page as one extractor block, so the same-block condition decided first. Each now has a case with two blocks. A listing whose lines are separate blocks would have had a space before every line break, and `breakAtBaselines` would then have skipped the break.
- **The check that an image was claimed was a copy,** and was deleted. A claimed image overlaps itself, so `overlapsAny` already excludes it.
- **The overlap threshold** had only a total overlap to test against, and now has a two-thirds one.
- **A table's identity in `sameTable`** had no two tables side by side to tell apart, and now has them.

## Consequences

- A web page printed to PDF converts with its inline code, its labels and its charts where they were written.
- The tagged path places an image by geometry where its tree is silent. A tree that leaves a picture out on purpose without marking it an artifact now has it written.
- A ligature in running text no longer survives as a ligature. A document about ligatures keeps the ones it sets alone.
- Chart values, a chart's axis labels read as text, are still one label to a paragraph. That is #18, reading a vector chart from its paths.

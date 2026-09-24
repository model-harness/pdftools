# 18. Record the glyph-inference architecture, and do not build it yet

Date: 2026-09-24

## Status

Accepted — as a recorded design with stated falsification criteria, not as an implementation.

## Context

A PDF can draw a glyph and give no reliable way to say what character it is. The font may carry no
`/ToUnicode`, a custom or damaged encoding, a symbolic cmap keyed by codes that mean nothing outside
the subset, or metadata that is simply wrong. `font.Glyph.Text` already documents the outcome: empty
is a real answer, and substituting U+FFFD would put noise in the output.

The proposal this ADR records is a **glyph-outline interpretation engine** with two separable tasks:

1. **Typeface inference** — which known face or metric family does this glyph resemble?
2. **Character inference** — given this outline and its context, which Unicode character is it?

With a per-glyph record: glyph ID → observed outline and metrics → likely typeface family and style
→ candidate Unicode values → confidence → evidence source. And the constraint that matters most:
**the deterministic path wins wherever `/ToUnicode` or encoding data exists.** The engine is for
missing, damaged, custom or misleading metadata.

The architecture is sound. This ADR is about whether it can be *built*, here, now.

## Decision

**The two tasks are separated, and they turn out to have opposite inputs. Typeface inference is
built (ADR 0017). Character inference is recorded and not built.**

### They are not one problem, and conflating them would fit neither

The proposal treats both as reading an outline. Only one of them can.

**Typeface inference, as this repo needs it, has no outline to read.** All 1,139 pages that need a
substitute need one because the font program is *absent* — that is the entire defect. There is
nothing to measure the shape of. The evidence is a name, a descriptor and a width array, and ADR
0017 builds the inference on exactly those. Outline-based typeface matching would apply to a
different question — "this embedded face claims to be Arial; is it?" — which nothing in this repo
asks.

**Character inference needs the opposite**: a font that *does* embed a program, so outlines exist,
whose encoding is missing or lying. Those are disjoint sets of fonts. A design spanning both with
one mechanism would serve neither.

### The population that needs character inference is, in this corpus, empty

Measured over all 12 documents: **2,935,462 glyphs drawn, 51 with no decodable meaning — 0.0017%.**

And the 51 do not survive inspection. Both fonts involved carry `/ToUnicode` *and* embed a program,
so neither is a metadata-absence case; tracing them found the codes spelled ordinary words
("Ac", "Ad", "ob", "se") and that the real extractor produces "Adobe Acrobat Reader", "approved",
"prepared" from that page. **The 51 were an artefact of the measuring probe**, which resolved `Tf`
names against the page's resources while the strings came from form XObjects and annotation
appearance streams with resource dictionaries of their own. The genuine count is at or near **zero**.

A heuristic fitted to zero samples cannot be falsified. This repository has been bitten by that
specific shape repeatedly — a rule with no input is not tested by a passing suite, it is absent from
it — and building an outline-to-Unicode engine would be the largest instance of it yet: essentially
OCR over vector outlines, serving a population that cannot demonstrate it works.

**The asymmetry of being wrong settles it.** An empty `Glyph.Text` is visibly no answer, and every
consumer already handles it. A *wrong* codepoint is indistinguishable from real text: it flows into
Markdown, into the OKF bundle, into search indexes, and nothing downstream can tell it from a
character the page actually drew. The failure mode is the one this whole phase refuses pages to
avoid, at the smallest granularity there is.

### But that is a fact about this corpus, not about PDFs

Stated plainly because the number above invites the wrong conclusion. The corpus is twelve
well-made, modern, tagged specification documents plus one arXiv paper. The population needing
character inference — pages scanned then OCR'd, custom-encoded subsets, pre-1.4 producers, damaged
files — is *precisely what this corpus excludes*, while DESIGN §9 says "30 years of bad producer
engines is the actual problem this repo exists to absorb."

So the design intent covers this. The test material does not exist yet, and that is the gap to
close first.

### The deterministic path is larger than it looks, and shrinks the heuristic's scope

Two deterministic routes exist that this repo has not taken, and both must be exhausted before any
inference is justified:

**Adobe's CMap resources** (`adobe-type-tools/cmap-resources`, BSD-3) are the predefined CMaps for
CID-keyed fonts. The `Adobe-*-UCS2` family maps CID to Unicode *by table*, which answers "what
character is this" for a CID font with no `/ToUnicode` — deterministically, with no candidates and
no confidence. Today `font/cmap` carries only Identity: `cmap.TwoByte` splits codes correctly for
any other predefined CMap and **maps nothing**, so a font naming `UniJIS-UCS2-H` yields correct code
widths and zero CIDs. The code says so already; this names the resource that fills it.

**Glyph names.** A Type 1 or CFF font's charstrings are named, and `/Differences` names glyphs by
`/Encoding`. A name like `uni0041` or `afii10017` or `Alpha` states the character outright, and the
Adobe Glyph List maps the rest. This is table lookup, not inference.

Both are deterministic, both are data rather than heuristics, and both are consistent with this
repo's standing preference for doing a thing with a library rather than guessing at it. **Whatever
population remains after those two is the real input to an inference engine, and it has not been
measured because the resources are not in place to measure it against.**

### What is built now: the evidence source, on the deterministic path

`Glyph.Text` says what a glyph means and not *how that was known*. The record the proposal
describes is right about that, and the part of it that pays off immediately is the evidence source —
`/ToUnicode`, or the encoding table, or a glyph name, or nothing.

It earns its place without any inference behind it, because it answers a question that cannot be
answered today: *how much of this document's text is known rather than assumed?* A document whose
text comes entirely from `/ToUnicode` and one where half comes from a guessed base encoding are
equally plausible on the page and not equally trustworthy. It is also the seam an inference engine
would attach to, which is why it goes in now rather than with the engine.

Confidence does not go in. On the deterministic path there is nothing to be uncertain about, and a
score of 1.0 on every glyph is a column that teaches a consumer to ignore the column.

## Consequences

**This is a design with falsification criteria rather than an aspiration, and the criteria are the
point.** A recorded design nobody validates rots. Character inference becomes justified when, and
only when:

1. A corpus exists with genuinely undecodable glyphs. `pdf.js`'s `test/pdfs` and pdfium's
   `testing/resources` are regression corpora of exactly these documents, and many are freely
   redistributable — so the gathering is a known, finishable task rather than a search.
2. The two deterministic routes above are implemented, and the population is re-measured *after*
   them. A number measured before them would overstate the case, possibly by all of it.
3. What remains is large enough to matter. If it is again 0.0017%, the answer is still no, and the
   measurement will have cost a day rather than a subsystem.

**If it is built, the shape is the one proposed**, with two amendments this ADR has argued: the
per-glyph record carries candidates *and* their evidence, because a candidate without its source
cannot be audited; and confidence appears only on the ranking path, never beside a determined
answer.

**`font.Glyph` gains a source, and that is an addition to a public type.** Its values are the
deterministic routes as they stand, so an inference route would add a value rather than change the
meaning of one — which is the property that makes this seam worth adding before the engine rather
than with it.

**Nothing about rendering changes.** A glyph whose text cannot be decoded already refuses the page in
`render/native`, and for substituted faces ADR 0017 makes the character route the *only* route. Both
stay true: this ADR adds no inference for either to fall back on.

# Reference fixtures — the yardstick

Thirty small PDFs, each exercising one thing, each beside the Markdown it *should*
produce. Everything here is ours: generated from the `.tex` sources in this
directory, one `.html` that Chrome prints, and `degrade.go`, which makes the scans.
All of it is licensed MIT with the rest of the repo, and therefore committable —
unlike the sponsored ISO documents in `docs/`, which are the corpus we measure
against but cannot redistribute.

## Why this exists

Every fidelity check before these fixtures compared our output to *itself*.
Counts reconciled, bundles round-tripped, escape rates held steady — all of which
detects drift and none of which detects being wrong from the start. Asked directly
whether the Markdown matched what was in the PDF, the honest answer was that
nothing had ever compared the two.

A gold file is that comparison. Not "did the output change" but "is the output
right", which is a question only an independently authored expectation can answer.

## Why the gold files are hand-written, not exported

The obvious shortcut is to open a PDF in Acrobat, export to text or XML, and call
that the expected output. That measures the wrong thing. Acrobat is a second
implementation, so agreeing with it proves we make the same choices it does, and
disagreeing with it starts an argument about which reader is right rather than
settling one. Worse, it makes the expectation a function of our tooling: an
exporter bug becomes a gold file, and then a test that enforces it.

So the gold files are written from the *source document's intent* — the `.tex` says
`\section{First Section}`, so the gold says `# First Section`. The chain from
intent to assertion never passes through a PDF reader, including ours.

Where our conventions are a choice rather than a fact, the gold follows the
choice `sink/markdown` documents: `#` per heading level, `**` and `*` for weight
and slant with whitespace kept outside the delimiters, `- ` for list items at two
spaces per level, and a backslash before `` \ ` * | ``. A gold file that disagreed
with the dialect would fail on formatting and tell us nothing about fidelity.

## Why untagged, and why `lmodern`

All but three are deliberately untagged, because most PDFs are and the layout
path is what reads them. `clauses.tex`, `tagged-lists.tex` and `tagged-table.tex` are
tagged, because a structure tree is the thing each exists to test.

`lists.tex` and `tagged-lists.tex` look like duplicates and are not. They cover the
same document feature through two different code paths that share no logic: the
untagged one infers an item from the bullet glyph a page draws, the tagged one reads
the `/Lbl` element a producer declares. Having only the untagged fixture is why the
tagged path shipped emitting `- ■ text` on 1363 items across six corpus files — a gap
no gold file was in a position to see.

Every source loads `lmodern` and none loads `[T1]{fontenc}`. Both matter, and both
were learned the hard way:

- Without `lmodern`, pdfTeX can fall back to PK bitmap fonts embedded as Type 3
  with glyph names like `a16` that carry no character meaning. There is no
  `/ToUnicode`, no `/ActualText`, and no `/Alt` to recover from — the text is not
  in the file, so the only route is OCR. A fixture that hit that path would be
  measuring an unrecoverable case rather than the one it was written for.
- `a16` is a *font-encoding slot*, not a Unicode code point. Building the same
  source under `[T1]` and `[OT1]` proves it: T1 emits
  `/Differences [16/a16/a17 21/a21 ...]` and OT1 emits no `/Differences` at all.
  Mapping `a<N>` to `rune(N)` would look right across ASCII and emit confident
  nonsense outside it — `a16` as U+0016, `a189` as `½`.

## The one fixture with two gold files

`metadata.pdf` has a `metadata.gold.md` like the others and a
`metadata.frontmatter.gold.md` beside it, because `md -frontmatter` is a second output
and nothing here had ever looked at it. That gap is half of why `objects/pdfcpu` could
drop the trailer's `/Info` entry and leave every document's title, author, subject,
keywords, creator, producer and dates empty for the project's whole life with the suite
green: no gold file ran with the flag on, and none of the other eleven sources sets any of
those four fields, so the values most likely to be wrong were also the ones no
expectation described. LaTeX does not write `\title` into `/Info` on its own — that is
`hyperref`'s job, through `\hypersetup` — so their absence was a property of the sources
rather than of the engine.

The frontmatter gold holds the whole file, fences and prose included, with one
substitution: `{{source}}` stands for the path given on the command line, which is the
caller's and not the document's. Everything else is asserted verbatim, dates included —
the PDF is pinned by SHA-256, so its `/CreationDate` is as fixed as its title.

Two details in `metadata.tex` are load-bearing and would look arbitrary:

- **`\pdfinfo{/ModDate ...}` sets a date that is not the build time.** pdfTeX honours it
  while still writing its own `/CreationDate`, so the two differ. Built with them equal,
  a reader that filled `modified` from `/CreationDate` emitted a value that matched, and
  that mutation survived — the assertion was true by coincidence.
- **The title carries a `": "` and the keywords a `","`.** The first forces YAML
  quoting (`": "` ends a key anywhere in a line) and the second must *not* be quoted,
  since a comma is only special inside a flow collection. Together they are the pair
  that distinguishes the quoting rule from a rule that quotes defensively.

The values arrive UTF-16BE with a byte-order mark, which is how `hyperref` writes them,
so this is also the only committed fixture pinning that decode path.

What the fixture is *for* was measured rather than assumed. Mutating the writer —
transposing the field order, quoting every value, dropping the blank line after the
fence, emitting empty keys instead of omitting them — is caught four times out of four
by `sink/markdown`'s own unit tests, so against the writer this gold file adds nothing.
Mutating the reader is the opposite: reading `Subject` from `/Keywords`, `Creator` from
`/Producer`, or `Modified` from `/CreationDate` each survives every test in the
repository and dies only here. Each emits a full block of plausible, well-formed, non-empty
values — which is precisely what a presence check, a key count, and a loader-validity
check all pass. Only an independently authored statement of which value belongs in
which field separates them.

## Scoring

Three tiers read these fixtures. `TestReferenceFidelity` asks whether the words of the
original twelve survive in order, and `TestReferenceExactMatch` asks which of those
fixtures match their gold file byte for byte. The third, the benchmark (ADR 0029),
scores every fixture on each dimension separately:

```sh
pdfspec bench testdata/reference
```

Its columns are CER, word recall and precision, reading order, block F1, and F1s for
headings, table cells and inline style. It also counts `[err]` and `[image]` marks.
`TestBenchmarkFloors` holds each fixture to the scores it had when it went in, so no
change can make a fixture worse without saying so. `TestBenchmarkTargets` reports how
far the means are from v1's targets, and fails on each dimension once the goal it
belongs to is closed.

The gold files written since the benchmark describe what the converter should do, not
what it does. Most of those fixtures score below 1 on something, and that gap is the
work.

## Layout

| File | Concern | Tagged |
|---|---|---|
| `headings.tex` | Heading sequence at three depths, in order | no |
| `text-styles.tex` | Bold, italic, bold-italic, monospace | no |
| `paragraphs.tex` | Paragraph breaks carried by the first-line indent alone | no |
| `lists.tex` | Bulleted and nested list items | no |
| `table.tex` | A ruled table's cell text | no |
| `image.tex` | A page whose only content is an image | no |
| `image-kinds.tex` | One image of each kind: text, a ruled table, a bar chart, a picture | no |
| `clauses.tex` | Numbered clause hierarchy via the structure tree | yes |
| `tagged-lists.tex` | Declared list markers, bulleted and numbered | yes |
| `tagged-table.tex` | A declared table grid with a header row | yes |
| `metadata.tex` | The information dictionary, as YAML frontmatter | no |
| `fractions.tex` | Stacked fractions, from a producer that strokes its bars | no |
| `superscripts.tex` | Raised and lowered runs of more than one glyph | no |
| `two-column.tex` | Two columns under a spanning title, a paragraph cut by the column break | no |
| `three-column.tex` | Three balanced columns, read a column at a time | no |
| `two-column-ragged.tex` | Two-column's break under ragged-right type, carried by a lower-case word | no |
| `unnumbered-headings.tex` | Three heading levels with no numbers, only size and weight | no |
| `table-unruled.tex` | A table held apart by alignment alone | no |
| `table-merged.tex` | A cell spanning two columns and one spanning two rows | no |
| `table-across-pages.tex` | One table split by a page break, its header repeated | no |
| `footnotes.tex` | Footnote marks and their notes, as GFM footnotes | no |
| `running-headers.tex` | A running header and folio, with a paragraph cut by the page break | no |
| `hyphenation.tex` | Words hyphenated at line ends, beside hyphenated compounds | no |
| `strikethrough.tex` | A phrase struck through by a drawn rule | no |
| `type3-unmapped.tex` | Bitmap Type 3 fonts with no Unicode mapping at all | no |
| `xelatex-article.tex` | A short article from XeTeX, with Identity-H CID fonts | no |
| `chrome-article.html` | The same article printed by Chrome | yes |
| `scan-*.tex` | Four of the above as degraded 200 dpi scans, with no text | no |

Each has a `.pdf` built from it and a `.gold.md` holding the expected Markdown.
The `.tex` is committed beside the `.pdf` so the fixture can be rebuilt and so the
gold file's derivation is auditable — a gold file whose source is missing is an
assertion nobody can check.

## Rebuilding

```sh
pdflatex -interaction=nonstopmode <name>.tex        # the untagged fixtures
lualatex -interaction=nonstopmode clauses.tex       # the tagged ones — see below
lualatex -interaction=nonstopmode tagged-lists.tex
lualatex -interaction=nonstopmode tagged-table.tex
xelatex  -interaction=nonstopmode xelatex-article.tex
chrome --headless --no-pdf-header-footer --print-to-pdf=chrome-article.pdf chrome-article.html
```

`table-across-pages.tex` needs two `pdflatex` runs, as every `longtable` does.

A scan is its source fixture rendered at 200 dpi, degraded, and placed on a page:

```sh
pdfspec render -o png -dpi 200 two-column.pdf
go run degrade.go png/page-1.png scan-two-column.jpg
pdflatex -interaction=nonstopmode scan-two-column.tex
```

`image-kinds.pdf` embeds three images built the same way from sources of known content, so
its gold file is what they hold:

```sh
pdflatex -interaction=nonstopmode image-kinds-text.tex     # and -table, -chart
pdfspec render -o png -dpi 200 image-kinds-text.pdf        # page-1.png is image-kinds-text.png
pdflatex -interaction=nonstopmode image-kinds.tex
```

The three intermediate PDFs are not kept: the PNGs are what `image-kinds.pdf` holds.

`degrade.go` seeds its noise, so the same render gives the same JPEG bytes. Each scan's
gold file is a copy of its source's, because the document is the same one.

The tagged fixtures need both a new enough kernel and the right engine.

A kernel older than its `latex-lab` fails with `\DebugTemplatesOff` undefined,
whichever engine runs it. That is a toolchain version skew and it is loud. On MiKTeX
it can also appear as two installations — an admin tree and a user-private one — where
the updated `pdflatex` reads the stale tree; `kpsewhich latex.ltx` says which tree is
actually in use, and that is what MiKTeX means by "User/administrator updates are
out-of-sync".

The engine requirement is the quiet one. **pdfTeX's tagging backend writes the wrong
`/MCID` into every `/MCR`** — for this document, `1` in all ten, against a content
stream that draws 0 through 9. Nine of the ten structure elements then join to
nothing: the clause titles survive because they come from `/T`, and every body
paragraph falls out of the tree. Extracting that build gave five headings with no
bodies. `lualatex` writes 0 through 9 and the same source extracts byte-for-byte
identical to `clauses.gold.md`. The same applies to `tagged-lists.tex`, whose whole
subject is the `/Lbl` elements a broken `/MCID` would detach.

That build is not a fixture worth keeping: no reader can distinguish it from a
document whose paragraphs are genuinely untagged, so it would pin our behaviour
against a broken producer instead of a correct one. The committed PDFs mean a clone
does not need LaTeX at all.

`tagged-table.tex` has a third requirement on top of those two, and it is quiet in the
same way: **a plain `tabular` under `tagging=on` declares every cell a `TD`**, header row
included. The first build of that fixture did exactly that, and the output was a table
with an empty header — correct behaviour for a headerless table, and the wrong shape to
be pinning, since 773 of the corpus's 788 tables declare an all-`TH` first row and only 11
declare none. `tagging-setup={table/header-rows={1}}` plus
`\usepackage{latex-lab-testphase-table}` is what makes row 1 `TH`. Confirm with
`pdfspec probe`, which reports the role counts: the committed build gives `TH=3 TD=6`, and
a build without the key gives `TD=9`.

# Reference fixtures — the yardstick

Eight small PDFs, each exercising one thing, each beside the Markdown it *should*
produce. Everything here is ours: generated from the `.tex` sources in this
directory, licensed MIT with the rest of the repo, and therefore committable —
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

All but two are deliberately untagged, because most PDFs are and the layout
path is what reads them. `clauses.tex` and `tagged-lists.tex` are tagged, because a
structure tree is the thing each exists to test.

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

## Layout

| File | Concern | Tagged |
|---|---|---|
| `headings.tex` | Heading sequence at three depths, in order | no |
| `text-styles.tex` | Bold, italic, bold-italic, monospace | no |
| `paragraphs.tex` | Paragraph breaks carried by the first-line indent alone | no |
| `lists.tex` | Bulleted and nested list items | no |
| `table.tex` | A ruled table's cell text | no |
| `image.tex` | A page whose only content is an image | no |
| `clauses.tex` | Numbered clause hierarchy via the structure tree | yes |
| `tagged-lists.tex` | Declared list markers, bulleted and numbered | yes |

Each has a `.pdf` built from it and a `.gold.md` holding the expected Markdown.
The `.tex` is committed beside the `.pdf` so the fixture can be rebuilt and so the
gold file's derivation is auditable — a gold file whose source is missing is an
assertion nobody can check.

## Rebuilding

```sh
pdflatex -interaction=nonstopmode <name>.tex        # the six untagged fixtures
lualatex -interaction=nonstopmode clauses.tex       # the tagged ones — see below
lualatex -interaction=nonstopmode tagged-lists.tex
```

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

# 3. One clause per file, and emit only OKF fields that are true

Date: 2026-08-03

## Status

Accepted

## Context

`sink/okf` turns a `doc.Outline` into an Open Knowledge Format v0.2 bundle. OKF is a
deliberately permissive format — markdown files with YAML frontmatter in a directory tree,
where `type` is the only universally required field, and §11 requires a consumer to accept a
bundle with missing optional fields, unknown `type` values, unknown extra keys, broken links,
and no `index.md` at all. Nothing in the format stops a producer from filling every slot.

That permissiveness is the decision to make. A bundle is read by a model that treats what it
says as fact, so every field is a claim, and a field whose value was invented to avoid leaving
it blank is a false claim that the format will happily carry and no validator will catch. The
shape of the bundle is the second decision: 981 clauses have to become files somehow, and the
arrangement determines whether a consumer can navigate the specification or only embed it.

`sink/okf` is also the first sink whose output is a public contract in a way the Markdown sink
is not. A markdown file is regenerated and diffed; a bundle is committed, linked to, and cited
by resource URI. The file layout and the resource form are expensive to change afterwards.

## Decision

**One clause is one concept document.** A section with children becomes a directory holding an
`index.md` plus a concept document named for the clause; a leaf becomes a single `.md` beside
its siblings. The parent's own prose cannot live in `index.md` — that filename is reserved by
§8 and carries no frontmatter — so it goes in a file named for the clause inside its own
directory, and the directory's `index.md` lists the parent alongside its children. A reader
who opens `7-4-filters/index.md` sees clause 7.4's prose and its ten subclauses, where a flat
directory of 981 files answers no question about which clause contains which.

**A field is emitted when it is trustworthy and omitted otherwise.** Concretely:

- `sources[].author` is omitted. OKF §7 makes an actor `<producer>/<version>`, `human:<id>`,
  or `process:<id>`, and consumers classify trust by detecting the `human:` prefix. ISO is an
  organization and has no actor form, so `author: ISO` — which `docs/DESIGN.md` §7 originally
  proposed — puts an unclassifiable value in the field a consumer uses to decide how much to
  trust the content. The document's own `/Author` is unusable for the same reason. Both facts
  are in the source PDF, which `resource` points at.
- `generated.by` is `pdfspec/<version>`, per the same convention. `pdfspec vX.Y.Z` does not
  parse as an actor.
- `generated.at` and `sources[].last_modified` are omitted when the source has no usable date.
  `isoDate` range-checks the PDF date string and returns nothing on `D:20241331`; a wrong date
  in a provenance field is worse than an absent one, and §11 makes the absence safe.
- `log.md` is skipped entirely when there is no date, rather than emitting an `unknown`
  heading — §9's structure *is* date headings, and a non-conformant log is worse than none.
- `status` is `draft` on every document. Nothing has verified that a clause's extracted text
  matches the page, and `status` defaults to `stable` when absent, so omitting it would assert
  the opposite of the truth. This is the one field where silence is the stronger claim.
- The clause number and page range go in `pdf_clause`, `pdf_page`, `pdf_page_last`, prefixed
  so a reader can see they are ours rather than fields to look up in the spec. §11 requires
  consumers to tolerate and preserve unknown keys.

**Unattributed content is a concept document that says so.** The outline's preamble becomes
`/front-matter.md` and each unplaced page becomes `/unplaced/page-NNNN.md`, both carrying
`pdf_unattributed: true` and a description stating that the placement is unknown. Attaching
this text to the nearest clause would file ISO 32000-2's entire Scope clause — which the
document draws outside any marked-content sequence — under "0.4 Changes introduced in ISO
32000-2:2020". Both are on by default: a bundle that drops content by default is a bundle
whose omissions nothing reports, and `Preamble` defaulted to false long enough to silently
lose WTPDF's CC-BY licence notice.

**Cross-references are resolved textually, for now.** A cue word (`clause`, `annex`, `see`,
`§ `) followed by a dotted clause number the document actually contains becomes a link.
Deliberately narrow: `in` and `of` precede more version numbers than clause numbers, and a
missed link costs a consumer one search where a wrong one sends it to the wrong clause and
looks authoritative doing it. Resolving from `/Annots` and `/Dests` is strictly better and is
still the target; it is not available because `doc.Outline` carries text and structure, and
annotations are neither.

**Paths are kept inside 150 characters, enforced.** Windows caps an absolute path at 260, so a
bundle may spend 260 less the destination the user names. `fit` picks the first of three
candidate names that fits — the full `7-4-1-general`, then `1-general` with the parent's number
dropped, then the bare number — rather than assuming a bound and discovering it as a write that
fails partway through a 1,193-file bundle with an error naming the path and not the cause.

## Consequences

Measured on the corpus:

| file | concepts | indexes | links | files | longest path |
|---|---|---|---|---|---|
| WTPDF 1.0 | 186 | 35 | 0 | 222 | 122 |
| ISO 32000-2 | 996 | 196 | 1,328 | 1,193 | 145 |

Zero letters or digits lost against the flat conversion on both. WTPDF resolving no links is
correct rather than a defect in the linker: that file's reading order draws the clause number
after a closing parenthesis, so its own references extract as `see ).8.2.6` and there is no
number adjacent to the cue to match. It is an extraction finding, recorded here because a
future reader will otherwise read the zero as a bug in `xref`.

**A conforming bundle can be nearly empty of metadata, and ours sometimes is.** The sponsored
ISO PDFs carry no `/Title`, so `docID` falls back to the filename and resource URIs come out as
`iso-32000-2-sponsored-ec3#7.4.8` rather than `iso32000-2:2020#7.4.8`. `Options.DocID` exists
for exactly this, and a caller who knows the designation should pass it — the heuristic reads
producer metadata and will be wrong for anything that is not a standard.

**The resource URI form and the file layout are now a contract.** A consumer citing
`iso32000-2:2020#7.5.8` or linking `/7-syntax/7-4-filters/7-4-8-dctdecode-filter.md` breaks if
either changes, which is what makes this an ADR rather than an implementation detail. Both are
derived from ancestry rather than from a counter, so they are stable across runs and across
insertions elsewhere in the document.

**Two escaping policies would have diverged.** `sink/markdown` exports `WriteBlocks`,
`YAMLString`, `InlineText`, and `LinkLabel` rather than `sink/okf` reimplementing them, so a
PDF dictionary in a clause body comes out identically in the flat conversion and in the bundle.
`LinkLabel` escapes both brackets unconditionally where the prose policy escapes `[` only when
it could open a link — correct for prose, wrong inside a label, and ISO 32000-2 has clause
titles containing brackets.

**`Bundle` returns files as values and `Write` is a thin wrapper.** A bundle *is* a directory
tree, so unlike `sink/markdown` this package touches the filesystem; keeping the rendering
separable is what lets the whole corpus acceptance suite run without writing a file.

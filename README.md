# pdftools

`pdfspec` turns PDFs into clean Markdown, from Go, without sending your documents to an AI
model.

```sh
curl -fsSL https://raw.githubusercontent.com/model-harness/pdftools/main/install.sh | sh
pdfspec md report.pdf > report.md
```

On Windows, install it from PowerShell with
`irm https://raw.githubusercontent.com/model-harness/pdftools/main/install.ps1 | iex`. Both
installers download the build for your machine from the latest release and check it against the
release's checksums before installing it, along with the copy of Tesseract it uses to read
scans. If you have Go, you can also run
`go install github.com/model-harness/pdftools/cmd/pdfspec@v1.0.1`, which builds `pdfspec` alone
and reads scans with a Tesseract on your PATH.

If you've ever tried this with an ordinary PDF library, you probably got back text with words
run together, two columns shuffled into one, and a page number stuck in the middle of a
sentence. That isn't the library being careless. It comes from what a PDF actually is.

## A PDF is a drawing of a page

PDF was designed in the era of print, to make a page look exactly the same on every screen
and every printer. It does that by describing the page as a drawing. A PDF is essentially a
list of vector instructions: draw the shape of the letter "T" here, in this font, at this size;
draw an "h" just to its right; draw a line under the heading; place this photo in that box.

That's perfect for printing, and awkward for everything else, because the drawing is all
there is. Nothing in the file says "this is a word", "this is a paragraph", or "these lines are
a table". A few details make that worse:

- Spaces usually aren't there. The gap between two words is just a bit of extra distance
  between two letter shapes. Whatever reads the file has to notice the gap and decide it's a
  space.
- Reading order isn't there. A two-column page is just shapes on the left and shapes on the
  right. The file doesn't say to finish the left column first.
- Tables are just lines and text. A table is some letters lined up, with or without ruled
  lines drawn around them.
- Headers, footers and page numbers sit right next to the content, with nothing marking
  them as different.
- Some fonts never say which letter a shape is. Sometimes the PDF draws a shape for "ti" or
  "fi" as one piece, with no record of the letters it stands for.
- A scanned page holds no text at all. It's a photograph of paper.

Some PDFs carry extra notes, called tags, that describe their own headings and reading order.
Most don't, so the structure has to be worked out from where things sit on the page.

## Why most tools get it wrong

Most converters read the letters in the order they were drawn and stop there. The letters come
out right but the document doesn't. We ran `pdfspec` against two other common tools using the
same 17-page research paper, and found the following:

| | characters found | words found | words over 25 characters | longest word |
|---|---|---|---|---|
| pdftools | 39,049 | 6,343 | 19 (0.30%) | 71 |
| pdftotext (Poppler) | 39,035 | — | — | — |
| pdfplumber 0.11.9 | 39,089 | 2,392 | 377 (15.8%) | 110 |

All three found the same characters, to within a fraction of a percent. The difference is in
the spaces. pdfplumber found just over a third as many words, and one in six of its "words"
ran past 25 characters, because it missed the gaps between them. Go libraries fare worse still: one we
tested found a space in only 0.01% of its text, and produced a single "word" 4,069 letters long.

The fashionable fix is to skip all this and show each page to an AI vision model. That works,
but it costs far more and takes far longer than reading text the file already contains. Worse,
when a model can't make something out, it tends to guess, and a confident guess looks exactly
like the real thing.

## How pdfspec does it

`pdfspec` treats the file as the best witness to its own content, and only looks further when
the file runs out.

First it reads what the file holds, and rebuilds the document from it. It works out the words
from the gaps between letters, the reading order from the columns, the headings from size and
weight, the tables from their lines or their alignment, and it sets aside the page furniture
such as running headers and page numbers. If the PDF carries tags, it follows them.

Where the file holds only pictures, such as a scanned page, a screenshot of a table, or a letter
whose font never said what it was, `pdfspec` can use Tesseract, a free text recognition
program, to read the image. Where even that isn't enough, it can ask an AI vision model for a
second opinion on a doubtful word, or for a description of a chart or a photo. Both are optional
and run on your own machine.

What it can't read, it doesn't guess. A letter nobody could read comes out as `[err]`, and an
image nobody could describe comes out as `[image]`. Each run also tells you which pages they
were on, so you know exactly where to look.

## Getting started

Convert a file:

```sh
pdfspec md report.pdf > report.md
```

If you're not sure what's inside a PDF, ask `probe` first. It describes the file without
converting it, including how `pdfspec` plans to read it and what it would need help with:

```
$ pdfspec probe autotagPDFInput.pdf
  pages       4
  ...
  structure   untagged
  ...
  path        layout
  engines     tesseract 5.5.3 eng; gemma-4-e4b via inferd
  needs       images: p2 2, p3 4, p4 2; glyphs with no value: p1 30, p2 24, p3 1, p4 3
```

Here `needs` says that pages 2 to 4 have pictures worth reading, and that every page has some
letters whose font never said what they were.

There are a few other commands for related jobs: `md -split` writes one file per page, `okf`
writes one file per section of a long specification, `images` pulls out the embedded pictures,
`render` turns pages into PNG or JPEG files, and `ocr` gives you more control over reading
scanned pages. Run `pdfspec <command> -h` for the options.

## Reading scans and pictures

Everything you need to read scans comes in the box. Each release of `pdfspec` ships with its own
copy of Tesseract, the open-source text recognizer, built from this repository with the English
model. The installers put it beside `pdfspec`, so a scanned page, a screenshot of a table, or a
letter whose font never said what it was gets read without anything else to install.

Describing pictures and charts is the one thing that needs more. For that, `pdfspec` asks a
vision model you run on your own machine, through [inferd](https://inferd.io). The model also
gives a second opinion on any word Tesseract isn't sure of. To install inferd along with
`pdfspec`, set `PDFSPEC_WITH_INFERD=1`:

```sh
curl -fsSL https://raw.githubusercontent.com/model-harness/pdftools/main/install.sh | PDFSPEC_WITH_INFERD=1 sh
```

On Windows, run `$env:PDFSPEC_WITH_INFERD = '1'` before the PowerShell command above. inferd runs
as your own background service and works on an ordinary CPU, no graphics card needed. The first
time it starts, it downloads its model, Gemma 4 (`gemma-4-e4b`), which is about 6 GB. You can also
install it yourself by following [inferd.io](https://inferd.io).

Without inferd, `pdfspec` reads everything else and writes `[image]` where a picture would have
been described. `pdfspec probe` tells you which engines it found.

If you'd rather use your own Tesseract, `pdfspec md -h` shows how to point at it. You can also
build the one `pdfspec` ships yourself, with `native/tesseract/build.sh --portable` or
`native/tesseract/build.ps1 -Portable`.

## How we know it's good

We keep a set of reference PDFs, each one built to test a single thing, such as two columns, a
table, a footnote, or a scanned page. For each one we wrote down by hand exactly what the
Markdown should be. Every change to `pdfspec` is scored against that set, and the build fails
if accuracy drops below the targets we set for this release: at most 3 wrong characters in a
hundred, and at least 97% of words found correctly, among others. Every one of those targets
passes today, on Linux and on Windows.

```sh
go test ./...      # runs the whole suite, with nothing else installed
```

## Using it from your own code

The command is a thin layer over a Go package, `github.com/model-harness/pdftools/pdfspec`,
which does the whole conversion. You get the same Markdown `pdfspec md` writes:

```go
err := pdfspec.Convert(ctx, os.Stdout, "report.pdf", pdfspec.DefaultOptions)
```

`pdfspec.DefaultOptions` reads scans and pictures with Tesseract and a vision model when it
finds them, just as the command does. The package reads no environment variables, so your
program decides: set `Tesseract` to the path of an executable, or to `"off"`, and `Inferd` to
an address, or to `"off"`. The warnings the command prints go to `Options.Log`, which is
discarded unless you give it a writer.

For more control, `Open` reads the file's own text, `Read` runs the engines, and `Markdown`
writes the result. In between, `Document.Doc` holds everything that was found: pages,
blocks, tables, images, and what was read off the pixels.

## Using it with coding agents

If you work with Claude Code, Codex or another coding agent, you can teach it to reach for
`pdfspec` whenever it needs what's inside a PDF, and to treat `[err]` and `[image]` as gaps
rather than guess at them. The skill is in [`skills/`](skills/), along with where each agent
expects to find it.

## Licence and further reading

`pdfspec` is MIT-licensed; see [`LICENSE`](LICENSE). Some of the test PDFs come from other
projects under their own licences, and [`docs/test.docs.md`](docs/test.docs.md) explains where
each one came from.

If you want to know how it works inside, [`docs/DESIGN.md`](docs/DESIGN.md) explains the
architecture, [`docs/adr/`](docs/adr/) records each significant decision with the measurements
behind it, and [`CHANGELOG.md`](CHANGELOG.md) lists what changed in each release.

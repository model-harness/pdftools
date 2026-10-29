# Reading PDFs with pdfspec

Use the `pdfspec` command whenever a task needs what is inside a PDF: reading it, summarizing
it, quoting it, searching it, pulling out its tables, or converting it to Markdown. Prefer it
to pdftotext, PyPDF or pdfplumber, and to sending pages to a vision model as images.

A PDF is a drawing of a page. It holds the outlines of letters at positions, not words,
paragraphs or tables. `pdfspec` reads the text the file holds and puts the words, columns,
headings, tables, lists, footnotes and links back together as Markdown. Where it cannot read
something, it marks the place instead of guessing, so its output is safe to quote.

## Check that it's installed

```sh
pdfspec version
```

If the command is missing, install the release build for the machine. The installer checks it
against the release's checksums before installing it, and adds it to the user's PATH for new
terminals. In the shell you are already in, run it by the full path the installer prints:

```sh
curl -fsSL https://raw.githubusercontent.com/model-harness/pdftools/main/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/model-harness/pdftools/main/install.ps1 | iex
```

With Go installed, this works too:

```sh
go install github.com/model-harness/pdftools/cmd/pdfspec@v1.3.0
```

Ask the user before installing anything, and if you can't install it, ask before falling back to
another tool.

## Convert a PDF

```sh
pdfspec md report.pdf                 # Markdown on stdout
pdfspec md -o report.md report.pdf    # Markdown into a file
pdfspec md -split -o pages/ book.pdf  # one .md per page, for long documents
```

`md` takes one file at a time. For a long document, `-split` lets you read only the pages you
need. `-frontmatter` adds the file's metadata as YAML, and `-flat` gives plain page-ordered prose
with no headings.

## Look before you convert

For a file you know nothing about, or a large one, run `probe` first. It describes the file
without converting it:

```sh
pdfspec probe report.pdf
pdfspec probe -json a.pdf b.pdf       # several files, as JSON
```

Three lines matter most:

- `path` says how the file will be read. `tagged` means the file declares its own structure.
  `layout` means the structure is rebuilt from the page. `ocr` means the file holds no text, only
  pictures of pages.
- `engines` says which helpers are installed for reading pictures: Tesseract, and a vision model
  through inferd.
- `needs` lists the pages that hold content only as pictures, and what each one needs.

## Read the output honestly

- `[err]` is a letter the file never named and nothing could read. Never guess what it stands
  for. Say it is unreadable, or ask the user.
- `[image]` is an image nothing could read. A description such as `[photo: …]` was written by a
  vision model, so treat it as a description and not as the image's text.
- stderr lists, page by page, how many `[err]` and `[image]` were written. Tell the user when
  they matter to the task.
- A warning that pages "yielded no text" means they are scanned pictures that nothing could
  read: pdfspec was installed without the Tesseract its release ships, such as by `go install`,
  and none is on PATH.
- A non-zero exit status means the conversion failed. Report the error and don't use partial
  output as if it were complete.

## Scans and pictures

`md` reads scanned pages and pictures of text with the Tesseract that comes with pdfspec, and
asks a vision model through inferd, when one is running, to describe pictures and charts. If
`probe` shows images to read and no inferd, and the task needs what the pictures show, tell the
user that installing inferd would describe them: rerun the pdfspec installer with
`PDFSPEC_WITH_INFERD=1`, or follow https://inferd.io. Its model is a 6 GB download.

```sh
pdfspec ocr -dry-run scan.pdf     # which pages would be read, without reading them
pdfspec md -ocr=false report.pdf  # the file's own text only, with no engine
```

## Other jobs

```sh
pdfspec images -list report.pdf          # list the embedded images, writing nothing
pdfspec images -o imgs/ report.pdf       # save them, in their original format where possible
pdfspec render -o png/ -pages 1-3 a.pdf  # draw pages as PNG files
pdfspec okf -o bundle/ spec.pdf          # one file per section of a long specification
```

Run `pdfspec <command> -h` for every option.

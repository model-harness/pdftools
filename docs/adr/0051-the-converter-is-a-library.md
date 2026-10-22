# 51. The converter is a library

Date: 2026-10-22

## Status

Accepted.

## Context

print2, a virtual printer, is to convert with pdftools rather than with its own Rust core. print2's platform code is C, a CUPS backend, a PAPPL application and a PDF service, plus C# for the Windows print task. All of it calls the core through ten C functions, such as `p2_pdf_to_markdown(pdf_path, md_path, ...)` and `p2_oxps_to_markdown`. None of it sees that the core is Rust.

pdftools was not something another program could convert with. The parts were public packages: `extract`, `ocr`, `layout`, `sectionize` and `sink/markdown`. But the conversion that runs them in order lived in `cmd/pdfspec`, a `main` package, which nothing can import. That was about 1,500 lines:

- opening the file;
- routing pages to OCR, and reading pages, images and glyphs with no value;
- the second reader;
- the outline or the inferred roles;
- every warning the conversion gives.

It wrote its messages to stderr and read `PDFSPEC_TESSERACT` and `PDFSPEC_INFERD` itself.

Porting pdftools to Rust was weighed and rejected. pdftools is 42,188 lines of Go, with 57,097 lines of tests and 50 decisions behind it. p2-core is 7,788 lines. Rewriting the larger side would mean re-measuring every figure the gold set holds. Rewriting the smaller side, behind C functions print2's adapters already call, changes none of that code.

## Decision

**Package `pdfspec` is the converter.** It runs the whole path from the file to Markdown, and the `pdfspec` command is flags and output on top of it.

```go
d, err := pdfspec.Open(path, pdfspec.DefaultOptions) // the file's own text
defer d.Close()
err = d.Read(ctx)    // what is only in pixels, with the engines Options name
err = d.Markdown(w)  // the outline, or the page order with inferred roles
```

`Convert(ctx, w, path, opt)` does the three in one call. `WritePage` writes a page alone, as `md -split` does. `Outline` is the tree's clause hierarchy, which `okf` builds on.

- **It reads no environment variable.** Every engine setting is a field of `Options`:
  - `Tesseract` is an executable, `""` for PATH, or `"off"`;
  - `Inferd` is an endpoint, `""` for the platform's, or `"off"`.

  The command maps `PDFSPEC_TESSERACT` and `PDFSPEC_INFERD` onto them in one function, `envEngines`. A program embedding the converter decides for itself, and a test states what it runs.
- **It writes no stderr.** What a reader of the output should know goes to `Options.Log`, which nil discards: pages that could not be read, what OCR read, and text left unread. The command sets it to stderr, so every message is the one it was, in the order it was.
- **`DefaultOptions` are `pdfspec md` with no flags,** as `extract.DefaultOptions` is the extractor's.
- **The `ocr` command's knobs are options:** `Pages`, `Threshold`, `Force`, and `Strict`, which makes an engine's failure Read's error rather than a warning. The page selection needs the page count, so `Document.Options` can be changed between `Open` and `Read`.
- **The rules other commands report are exported:**
  - `Route` and `PageNeeds`, which say what `Read` would read;
  - `FindEngines`, for the engines it would use;
  - `Unmapped`, `ErrTotal` and `ErrPages`, for glyphs with no value;
  - `InferRoles` and `OpenRasterizer`.

  `probe`, `ocr -dry-run`, `okf` and `render` answer with the converter's own rules rather than copies of them.
- `oneLine`, which folds a parser's error onto one line, moves to `internal/oneline`, since the library's page warning and probe's page rows both use it.

The tests moved with the code: 172 tests before, 173 after. One is renamed, `TestReadReadsImagesOnPagesWithText`, which now tests `Open` and `Read`. One is new, `TestEngineFlagsFallBackToTheEnvironment`, for the mapping the command now owns. A test that captured stderr from a library function now hands it a writer. A library test names its engines through `offline()`, since no `TestMain` turns them off from the environment.

**print2 follows in steps, each a separate change:**

1. **An exported C library.** A Go library built with `-buildmode=c-shared` exports print2's `p2_*` functions over `pdfspec`. Its adapters link it unchanged, on Windows, Linux and macOS.
2. **OXPS and PWG Raster in the engine.** These are ported from p2-core's `oxps.rs` and `pwg.rs`, so a Windows job goes through the same pipeline as a PDF.
3. **Tesseract in-process,** through its C interface. Step 1 needs cgo already, and this answers whether an MSIX task may start `tesseract.exe` by not asking it.
4. **Compare before deleting.** p2-core's own reader, tables and lists are compared with pdftools' on print2's corpus before any is removed.

## Acceptance

- **The suite:** `go test ./...` and `go vet` pass on amd64 and 386, and `staticcheck -checks U1000` finds nothing the move left unused.
- **Old against new:** the command built from the commit before this change and the one after were run over every PDF here, the 62 committed fixtures and the 12 corpus documents:
  - `md` and `md -flat` with no engine;
  - `ocr -dry-run` and `probe -json`;
  - on the fixtures, `md` and `ocr` with Tesseract 5.5.3, and `md -ocr=false`.

  That is 482 runs. Stdout is identical in all of them once probe's own timing, `probe_ms`, is masked, as it differed in 42. Stderr is identical, with run times masked, in all but 13.
- **The intended change, those 13:** they are all `ocr` with Tesseract, and each gained lines and lost none. The `ocr` command now reports what `md` reports after reading: glyphs left `[err]` in 7 of them, and a document that yielded no text in the other 6. Both now come from `Read`, where before only `md` said so.

## Consequences

- A Go program converts with `import "github.com/model-harness/pdftools/pdfspec"`, and gets what `pdfspec md` writes.
- The command and the library cannot drift. Bench scores `pdfspec.Convert`, and `md`, `ocr` and `okf` read through `Open`. `probe` reads the file its own way, for what the converter does not need, and asks the converter's rules for the rest.
- A new engine setting is a field of `Options` first. An environment variable for it is the command's business.

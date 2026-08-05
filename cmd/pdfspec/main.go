// Command pdfspec is the CLI for the pdftools toolkit.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

const usage = `pdfspec - PDF tooling that does not fight you

usage: pdfspec <command> [flags] <file.pdf>

commands:
  md       convert a PDF to Markdown
  okf      convert a PDF to an Open Knowledge Format bundle, one file per clause
  images   extract embedded images, original codec preserved where possible
  render   rasterize pages to PNG or JPEG
  ocr      convert a PDF to Markdown, recognizing pages that carry no text
  probe    report what a PDF contains and which extraction path it will take
  version  print version

run "pdfspec <command> -h" for command flags
`

// version, when non-empty, overrides the version this binary reports. It is the
// -ldflags -X hook and nothing assigns it in Go code, which is the only way that hook
// works: the linker rewrites the initial value of a string var, so a var initialized by
// a function call gets that value written and then immediately overwritten at package
// init. This started as `var version = buildVersion()` and silently did exactly that —
// `-X main.version=9.9.9` built cleanly and printed the pseudo-version.
var version string

// buildVersion is what the version verb prints and what stamps every OKF bundle's
// generated.by, so it has to be right in a `go install`ed binary and not only in a
// release built by a script we control. version alone was that script's hook and no
// script exists, so every install reported 0.0.0-dev and wrote it into its own output.
//
// debug.ReadBuildInfo carries the version the toolchain resolved. Measured, all four
// modes: `go install ...@v0.1.0` gives the tag, `@main` a pseudo-version, a plain
// `go build` in this checkout a VCS-derived pseudo-version with "+dirty" when the tree
// is, and `go run` "(devel)" — which is the one case with no version to report and the
// only reason for the 0.0.0-dev fallback.
//
// The v is stripped because generated.by reads "pdfspec/0.1.0": the actor form in the
// OKF spec is a name and a version, not a Go module selector.
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "0.0.0-dev"
	}
	return strings.TrimPrefix(info.Main.Version, "v")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "md":
		err = runMD(args)
	case "okf":
		err = runOKF(args)
	case "images":
		err = runImages(args)
	case "render":
		err = runRender(args)
	case "ocr":
		err = runOCR(args)
	case "probe":
		err = runProbe(args)
	case "version", "-v", "--version":
		fmt.Printf("pdfspec %s\n", buildVersion())
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "pdfspec: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "pdfspec: %v\n", err)
		os.Exit(1)
	}
}

func runProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	pages := fs.Bool("pages", false, "include per-page detail")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec probe [-json] [-pages] <file.pdf>...\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return fmt.Errorf("no input file")
	}

	// Expand globs: the shell on Windows does not, and probing a whole corpus
	// at once is the normal use.
	var files []string
	for _, a := range fs.Args() {
		m, err := filepath.Glob(a)
		if err != nil || len(m) == 0 {
			files = append(files, a)
			continue
		}
		files = append(files, m...)
	}

	return probe(files, *asJSON, *pages)
}

// Command pdfspec is the CLI for the pdf-spec toolkit.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const usage = `pdfspec - PDF tooling that does not fight you

usage: pdfspec <command> [flags] <file.pdf>

commands:
  md       convert a PDF to Markdown
  okf      convert a PDF to an Open Knowledge Format bundle, one file per clause
  probe    report what a PDF contains and which extraction path it will take
  version  print version

run "pdfspec <command> -h" for command flags
`

var version = "0.0.0-dev"

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
	case "probe":
		err = runProbe(args)
	case "version", "-v", "--version":
		fmt.Printf("pdfspec %s\n", version)
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

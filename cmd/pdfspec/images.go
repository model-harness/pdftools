package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pdfimage "github.com/3rg0n/pdf-spec/image"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
)

func runImages(args []string) error {
	fs := flag.NewFlagSet("images", flag.ExitOnError)
	out := fs.String("o", "", "output directory (required)")
	list := fs.Bool("list", false, "report what the file contains and write nothing")
	masks := fs.Bool("masks", true, "write soft masks as their own files")
	minPx := fs.Int("min", 0, "skip images smaller than this many pixels on both sides")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec images [-o dir] [-list] [-masks] [-min n] <file.pdf>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("images takes exactly one input file")
	}
	if *out == "" && !*list {
		return fmt.Errorf("-o <dir> is required, or -list to report without writing")
	}

	in := fs.Arg(0)
	s, err := pcstore.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	ims, err := pdfimage.NewReader(s).Images()
	if err != nil {
		return err
	}
	if *list {
		return listImages(os.Stdout, ims)
	}
	return writeImages(ims, *out, *masks, *minPx)
}

// listImages reports what the file holds without writing anything.
//
// This is the same role probe plays for text: the first question about a PDF's
// images is what codecs and sizes are in it, and answering that should not require
// writing several hundred files to find out.
func listImages(w io.Writer, ims []*pdfimage.Image) error {
	if len(ims) == 0 {
		fmt.Fprintln(w, "no images")
		return nil
	}
	byCodec := map[string]int{}
	alpha, premul, unsupported := 0, 0, 0
	for _, im := range ims {
		byCodec[im.Codec.String()]++
		if im.Alpha() {
			alpha++
		}
		if im.Premultiplied() {
			premul++
		}
		if _, err := im.Ext(); err != nil {
			unsupported++
		}
		fmt.Fprintf(w, "p%-5d %-14s %5dx%-5d %-2d bpc  %-12s %s\n",
			im.Page, im.Name, im.Width, im.Height, im.BitsPerComponent,
			codeSpace(im), im.Codec)
	}
	fmt.Fprintf(w, "\n%d images  %s\n", len(ims), joinCounts(byCodec))
	fmt.Fprintf(w, "%d with transparency, %d premultiplied against a matte", alpha, premul)
	if unsupported > 0 {
		// Said plainly, because these are the ones a write run will not produce.
		fmt.Fprintf(w, ", %d in a codec this build cannot write", unsupported)
	}
	fmt.Fprintln(w)
	return nil
}

func codeSpace(im *pdfimage.Image) string {
	if im.Stencil {
		return "stencil"
	}
	if im.ColorSpaceFamily == "" {
		return "?"
	}
	return string(im.ColorSpaceFamily)
}

func joinCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, m[k])
	}
	return strings.Join(parts, " ")
}

// writeImages extracts every image into dir.
//
// One unsupported or damaged image does not fail the run — a 1,023-page
// specification with one JPX among 224 images should still yield the other 223 —
// but the count of what was skipped is reported, because a silent partial
// extraction is indistinguishable from a complete one.
func writeImages(ims []*pdfimage.Image, dir string, masks bool, minPx int) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	// Zero-padded to the count's width so a directory listing is in extraction
	// order; page 10 sorting before page 2 makes a large corpus unusable in a file
	// browser, the same reason md -split pads.
	width := len(fmt.Sprint(len(ims)))
	wrote, skipped, small := 0, 0, 0
	var unsupported []string

	for i, im := range ims {
		if minPx > 0 && im.Width < minPx && im.Height < minPx {
			small++
			continue
		}
		n, err := writeOne(dir, fmt.Sprintf("img-%0*d-p%d", width, i+1, im.Page), im)
		if err != nil {
			if errors.Is(err, pdfimage.ErrUnsupported) {
				unsupported = append(unsupported, im.Codec.String())
			}
			skipped++
			continue
		}
		wrote += n

		if masks && im.SMask != nil {
			// The mask is written as its own file rather than composited in. For a
			// premultiplied image that is not a convenience: the base samples are
			// not the colours they look like, and both layers are needed to recover
			// what they were.
			if m, err := writeOne(dir, fmt.Sprintf("img-%0*d-p%d-mask", width, i+1, im.Page), im.SMask); err == nil {
				wrote += m
			}
		}
	}

	fmt.Fprintf(os.Stderr, "wrote %d files from %d images to %s\n", wrote, len(ims), dir)
	if small > 0 {
		fmt.Fprintf(os.Stderr, "skipped %d below -min %d\n", small, minPx)
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "skipped %d that could not be written (%s)\n",
			skipped, joinCounts(tally(unsupported)))
	}
	return nil
}

// writeOne writes a single image, returning the number of files it produced: 1,
// or 0 when the codec cannot be written.
func writeOne(dir, base string, im *pdfimage.Image) (int, error) {
	ext, err := im.Ext()
	if err != nil {
		return 0, err
	}
	path := filepath.Join(dir, base+"."+ext)
	err = writeFile(path, func(w io.Writer) error {
		return pdfimage.Encode(w, im)
	})
	if err != nil {
		// A partial file is worse than none: it looks like a successful extraction
		// and fails only when something tries to open it.
		_ = os.Remove(path)
		return 0, err
	}
	return 1, nil
}

func tally(s []string) map[string]int {
	m := map[string]int{}
	for _, v := range s {
		m[v]++
	}
	return m
}

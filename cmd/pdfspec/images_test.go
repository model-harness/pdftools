package main

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pdfimage "github.com/3rg0n/pdf-spec/image"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
)

// The corpus numbers this package was scoped against, measured across every
// tracked and sponsored PDF present. They are a baseline, not a guess: a change in
// any of them means image discovery moved, and the direction says which way.
//
// Deduplication is what makes these numbers small. ISO 32000-2 draws its images
// across 1,023 pages, and counting placements instead of objects would report
// thousands.
const (
	wantISOImages = 224
	wantISOJPEG   = 49
	wantISORaw    = 175 // 171 Flate + 4 unfiltered
)

func TestISOImageInventory(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	ims, err := pdfimage.NewReader(s).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(ims) != wantISOImages {
		t.Errorf("images = %d, want %d", len(ims), wantISOImages)
	}

	byCodec := map[pdfimage.Codec]int{}
	withMask, premul := 0, 0
	for _, im := range ims {
		byCodec[im.Codec]++
		if im.SMask != nil {
			withMask++
		}
		if im.Premultiplied() {
			premul++
		}
		if im.Width <= 0 || im.Height <= 0 {
			t.Errorf("%s p%d: %dx%d — a zero dimension should have been rejected",
				im.Name, im.Page, im.Width, im.Height)
		}
		if im.Codec == pdfimage.CodecRaw && im.Components == 0 && !im.Stencil {
			t.Errorf("%s p%d: raw samples with no component count (%s)",
				im.Name, im.Page, im.ColorSpaceFamily)
		}
		if strings.HasSuffix(string(im.Name), ".smask") {
			t.Errorf("a soft mask was reported as a top-level image: %s", im.Name)
		}
	}
	if byCodec[pdfimage.CodecJPEG] != wantISOJPEG {
		t.Errorf("jpeg = %d, want %d", byCodec[pdfimage.CodecJPEG], wantISOJPEG)
	}
	if byCodec[pdfimage.CodecRaw] != wantISORaw {
		t.Errorf("raw = %d, want %d", byCodec[pdfimage.CodecRaw], wantISORaw)
	}
	// Neither codec appears anywhere in this corpus, which is why they are named
	// and refused rather than implemented. If one ever shows up here, the JBIG2
	// port moves from Phase 6 to now.
	if n := byCodec[pdfimage.CodecJBIG2] + byCodec[pdfimage.CodecJPX]; n != 0 {
		t.Errorf("%d JBIG2/JPX images appeared: the corpus premise changed", n)
	}
	t.Logf("%d images, %d with a soft mask, %d premultiplied", len(ims), withMask, premul)
}

// Every image the corpus contains must encode to a file that decodes. This is the
// assertion the unit tests cannot make: they are built from hand-written
// dictionaries and would never contain the shapes a real producer emits.
func TestCorpusImagesEncodeToValidFiles(t *testing.T) {
	total, encoded, unsupported := 0, 0, 0
	for _, name := range corpusFiles() {
		s, err := pcstore.Open(corpusFile(t, name))
		if err != nil {
			t.Errorf("%s: open: %v", name, err)
			continue
		}
		ims, err := pdfimage.NewReader(s).Images()
		if err != nil {
			t.Errorf("%s: Images: %v", name, err)
			_ = s.Close()
			continue
		}
		for _, im := range ims {
			total++
			var buf bytes.Buffer
			if err := pdfimage.Encode(&buf, im); err != nil {
				if errors.Is(err, pdfimage.ErrUnsupported) {
					unsupported++
					continue
				}
				t.Errorf("%s %s p%d (%s %dx%d): Encode: %v",
					name, im.Name, im.Page, im.Codec, im.Width, im.Height, err)
				continue
			}
			if buf.Len() == 0 {
				t.Errorf("%s %s p%d: encoded to zero bytes", name, im.Name, im.Page)
				continue
			}
			encoded++

			switch im.Codec {
			case pdfimage.CodecJPEG:
				// The passthrough promise: the output is the stream's own bytes.
				if !bytes.Equal(buf.Bytes(), im.Data) {
					t.Errorf("%s %s p%d: JPEG was re-encoded (%d bytes in, %d out)",
						name, im.Name, im.Page, len(im.Data), buf.Len())
				}
			default:
				cfg, err := png.DecodeConfig(bytes.NewReader(buf.Bytes()))
				if err != nil {
					t.Errorf("%s %s p%d: the PNG written does not decode: %v",
						name, im.Name, im.Page, err)
					continue
				}
				// The dictionary's declared size is what a caller was told to
				// expect, so the file must match it rather than the sample count.
				if cfg.Width != im.Width || cfg.Height != im.Height {
					t.Errorf("%s %s p%d: PNG is %dx%d, dictionary says %dx%d",
						name, im.Name, im.Page, cfg.Width, cfg.Height, im.Width, im.Height)
				}
			}
		}
		_ = s.Close()
	}
	if total == 0 {
		t.Skip("corpus absent")
	}
	t.Logf("%d images, %d encoded, %d unsupported", total, encoded, unsupported)
	if unsupported != 0 {
		t.Errorf("%d images could not be encoded; the corpus has no JBIG2 or JPX, "+
			"so every one of them should have been writable", unsupported)
	}
}

// The soft-mask population, which the survey found to be the dominant case at 143
// of 245 images rather than the edge case the roadmap implied.
func TestCorpusSoftMasks(t *testing.T) {
	masks, premul, sized, dctMask := 0, 0, 0, 0
	for _, name := range corpusFiles() {
		s, err := pcstore.Open(corpusFile(t, name))
		if err != nil {
			continue
		}
		ims, err := pdfimage.NewReader(s).Images()
		if err != nil {
			_ = s.Close()
			continue
		}
		for _, im := range ims {
			if im.SMask == nil {
				continue
			}
			masks++
			sm := im.SMask
			if sm.Matte != nil {
				premul++
			}
			if sm.Width != im.Width || sm.Height != im.Height {
				sized++
			}
			if sm.Codec == pdfimage.CodecJPEG {
				dctMask++
			}
			// A soft mask is DeviceGray by definition (§11.6.5.3), and a mask that
			// is not would silently supply the wrong channel as alpha.
			if sm.Codec == pdfimage.CodecRaw && sm.Components != 1 {
				t.Errorf("%s %s p%d: soft mask has %d components, want 1",
					name, im.Name, im.Page, sm.Components)
			}
		}
		_ = s.Close()
	}
	if masks == 0 {
		t.Skip("corpus absent")
	}
	if masks != 143 {
		t.Errorf("soft masks = %d, want 143", masks)
	}
	if premul != 136 {
		t.Errorf("premultiplied (/Matte) = %d, want 136", premul)
	}
	t.Logf("%d masks: %d premultiplied, %d differently sized, %d DCT-encoded",
		masks, premul, sized, dctMask)
}

// The verb end to end, including the naming and the mask files.
func TestImagesVerbWritesFiles(t *testing.T) {
	// The smallest corpus file with images, so the test stays quick.
	path := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	dir := t.TempDir()
	if err := runImages([]string{"-o", dir, path}); err != nil {
		t.Fatalf("runImages: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no files written")
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- test temp dir
		if err != nil {
			t.Fatal(err)
		}
		if len(b) == 0 {
			t.Errorf("%s is empty", e.Name())
		}
		// The extension must describe the contents, or a consumer opens the wrong
		// decoder and gets a corrupt-file error for a perfectly good image.
		switch filepath.Ext(e.Name()) {
		case ".png":
			if _, err := png.DecodeConfig(bytes.NewReader(b)); err != nil {
				t.Errorf("%s does not decode as PNG: %v", e.Name(), err)
			}
		case ".jpg":
			if len(b) < 2 || b[0] != 0xFF || b[1] != 0xD8 {
				t.Errorf("%s has no JPEG SOI marker", e.Name())
			}
		default:
			t.Errorf("%s: unexpected extension", e.Name())
		}
		// Zero-padded and page-attributed, so a directory listing is in extraction
		// order rather than lexicographic order over unpadded numbers.
		if !strings.HasPrefix(e.Name(), "img-") || !strings.Contains(e.Name(), "-p") {
			t.Errorf("%s does not follow the img-NNN-pN naming", e.Name())
		}
	}
}

// -list writes nothing. A report that also produced several hundred files would
// defeat its purpose, which is answering "what is in here" cheaply.
func TestImagesListWritesNothing(t *testing.T) {
	path := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	dir := t.TempDir()
	if err := runImages([]string{"-list", "-o", dir, path}); err != nil {
		t.Fatalf("runImages -list: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("-list wrote %d files", len(entries))
	}
}

// -o is required for a write run, because the alternative — several hundred binary
// files on stdout — is never what anyone meant.
func TestImagesRequiresOutputDir(t *testing.T) {
	path := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	if err := runImages([]string{path}); err == nil {
		t.Error("want an error with no -o and no -list")
	}
}

// -min skips small images, which on a specification means the inline icons and
// rules that are not what anyone extracting images wants.
func TestImagesMinFilter(t *testing.T) {
	path := corpusFile(t, "ISO-TS-32005-2023-sponsored.pdf")
	all, small := t.TempDir(), t.TempDir()
	if err := runImages([]string{"-o", all, path}); err != nil {
		t.Fatal(err)
	}
	if err := runImages([]string{"-o", small, "-min", "100000", path}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadDir(all)
	b, _ := os.ReadDir(small)
	if len(a) == 0 {
		t.Skip("no images in this file")
	}
	if len(b) >= len(a) {
		t.Errorf("-min 100000 kept %d of %d files", len(b), len(a))
	}
}

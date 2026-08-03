package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pdfimage "github.com/3rg0n/pdf-spec/image"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
	"github.com/3rg0n/pdf-spec/tag"
)

// The reference fixtures, which are a different kind of test input from the corpus in
// docs/.
//
// The corpus is eleven documents from one family of producers — ISO and the PDF
// Association, all tagged, all standards prose — and every threshold in this suite was
// established on it. That makes it a good regression baseline and a bad witness: a
// heuristic tuned on it cannot be shown to generalize by running it again.
//
// These files come from upstream projects that ship them deliberately as reference
// inputs, so each one is a case someone else decided was worth having a fixture for.
// They are committed (see testdata/manifest.json for provenance and testdata/fetch.ps1
// to re-verify the bytes), so unlike the corpus tests these never skip.
//
// Two of them were earning their keep before this file existed. ocr-ed.pdf's upstream
// .txt is the only ground truth in the repository — a statement of what the right
// answer is, written by someone who was not us — and comparing against it is what found
// the OCR-layer defect that TestFixtureOCRMatchesGroundTruth now pins. sampleInvoice.pdf
// is the first independent witness for probe's producer-stub check, and it is Adobe's
// own sample.

const fixtureDir = "../../testdata"

func fixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(fixtureDir, name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("reference fixture missing: %s — run `pwsh testdata/fetch.ps1 -Download`", name)
	}
	return p
}

// TestFixtureManifestMatchesTree keeps the two from drifting apart.
//
// The manifest is the provenance record: it is what says these bytes came from a named
// upstream commit rather than from somewhere unrecorded. A file in the tree that the
// manifest does not list has no provenance, and a manifest entry with no file makes
// fetch.ps1 report a phantom. Neither is visible without this check, because every
// other test here names its file directly.
func TestFixtureManifestMatchesTree(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(fixtureDir, "manifest.json")) // #nosec G304 -- fixed repo path
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Sources []struct {
			Repo    string `json:"repo"`
			Commit  string `json:"commit"`
			License string `json:"license"`
			Files   []struct {
				Path     string `json:"path"`
				Upstream string `json:"upstream"`
				SHA256   string `json:"sha256"`
			} `json:"files"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}

	listed := map[string]bool{}
	for _, src := range m.Sources {
		if src.Repo == "" || src.Commit == "" || src.License == "" {
			t.Errorf("source %q: repo, commit, and license are the provenance record and none may be empty", src.Repo)
		}
		// A commit has to be a full SHA-1. A branch name or a short hash resolves to
		// different bytes over time, which is the whole thing the manifest prevents.
		if len(src.Commit) != 40 {
			t.Errorf("%s: commit %q is not a full 40-character SHA", src.Repo, src.Commit)
		}
		for _, f := range src.Files {
			if f.Upstream == "" || len(f.SHA256) != 64 {
				t.Errorf("%s: %q has no upstream path or no SHA-256", src.Repo, f.Path)
			}
			if listed[f.Path] {
				t.Errorf("%q listed twice", f.Path)
			}
			listed[f.Path] = true
			if _, err := os.Stat(filepath.Join(fixtureDir, f.Path)); err != nil {
				t.Errorf("%q is in the manifest but not in the tree", f.Path)
			}
		}
	}

	var untracked []string
	err = filepath.WalkDir(fixtureDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(fixtureDir, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		switch rel {
		case "manifest.json", "fetch.ps1":
			return nil
		}
		if !listed[rel] {
			untracked = append(untracked, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(untracked) > 0 {
		t.Errorf("in the tree but not in the manifest, so with no recorded provenance: %v", untracked)
	}
	if len(listed) == 0 {
		t.Fatal("the manifest lists nothing")
	}
	t.Logf("%d fixtures, all with provenance", len(listed))
}

// TestFixtureOCRMatchesGroundTruth is the only test in this repository that checks
// output against an answer someone else wrote down.
//
// Upstream ships OCR/ocr-ed.txt beside OCR/ocr-ed.pdf: a scanned page, the same page
// after Tesseract, and the text the OCR layer holds. Every other assertion here is a
// baseline this project measured from its own behavior, which catches a regression but
// cannot catch a mistake that was present when the baseline was taken. This one can, and
// did — the text was word-for-word right while every line of it came out wrapped in
// backticks, because Tesseract's GlyphLessFont declares FixedPitch and the sink read
// that as "code". No metric in the suite would have reported it: character counts,
// space ratios, and word lengths are all identical either way.
//
// Compared on word sequence rather than byte-for-byte, because the upstream file is
// PyMuPDF's own layout-preserving rendering — it pads with spaces to place text on the
// page — and Markdown's is not. The words and their order are the claim; the whitespace
// between them is each renderer's business.
func TestFixtureOCRMatchesGroundTruth(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(fixtureDir, "pymupdf-utilities/ocr-ed.txt")) // #nosec G304 -- fixed repo path
	if err != nil {
		t.Fatal(err)
	}
	md := mdOut(t, fixture(t, "pymupdf-utilities/ocr-ed.pdf"))

	wantWords, gotWords := strings.Fields(string(want)), strings.Fields(md)
	if len(gotWords) != len(wantWords) {
		t.Fatalf("%d words, ground truth has %d\n got:  %v\n want: %v",
			len(gotWords), len(wantWords), gotWords, wantWords)
	}
	for i := range wantWords {
		if gotWords[i] != wantWords[i] {
			t.Errorf("word %d = %q, ground truth says %q", i, gotWords[i], wantWords[i])
		}
	}

	// The specific failure that made this test worth writing. An OCR layer is a
	// fixed-pitch font by declaration and invisible by rendering mode, and reading the
	// first fact without the second turns a scanned document into one long code span.
	if strings.Contains(md, "`") {
		t.Errorf("a code span in a scanned document's OCR layer: %q", md)
	}
}

// A second OCR engine, so the fix above is not a fix for Tesseract.
//
// PDF_XChange writes its invisible layer in a font that is not fixed-pitch, which is
// exactly why it belongs here: it holds the hidden-text handling to the same output on a
// file where the FixedPitch flag is absent. Its OCR is visibly worse than Tesseract's
// ("bindingsfar", "Sep17") and that is not this project's business to fix — what is
// asserted is that the text arrives and arrives unmarked.
func TestFixtureSecondOCREngine(t *testing.T) {
	md := mdOut(t, fixture(t, "pymupdf-utilities/PDF_XChange-OCRed.pdf"))
	if strings.Contains(md, "`") {
		t.Errorf("a code span in an OCR layer: %q", md)
	}
	for _, want := range []string{"PyMuPDF", "Documentation", "1.18.19", "McKie"} {
		if !strings.Contains(md, want) {
			t.Errorf("%q missing from the OCR text: %q", want, md)
		}
	}
}

// A scan with no text layer produces nothing, and that is the correct answer.
//
// scanned.pdf is the same page as ocr-ed.pdf before Tesseract ran: one DCT image and no
// fonts. Emitting nothing is right, and the pair is the clearest statement of what the
// Phase 4 OCR path is for — the image is there to be read and this pipeline cannot read
// it. What must not happen is an error or a page of noise from glyph-shaped drawing
// operators.
func TestFixtureScanWithoutTextLayerIsEmpty(t *testing.T) {
	path := fixture(t, "pymupdf-utilities/scanned.pdf")
	if md := strings.TrimSpace(mdOut(t, path)); md != "" {
		t.Errorf("text from a page with no text layer: %q", md)
	}
	ims := readImages(t, path)
	if len(ims) != 1 {
		t.Fatalf("%d images, want 1: the page a scan holds is the whole document", len(ims))
	}
	if ims[0].Codec != pdfimage.CodecJPEG {
		t.Errorf("codec = %v, want jpeg", ims[0].Codec)
	}
}

// TestFixtureRouting is probe's answer for every fixture, which is the routing decision
// the whole pipeline hangs off.
//
// The corpus cannot test this. Ten of its eleven files are tagged standards documents,
// so the tagged branch is the only one they exercise and the other three were reachable
// only through the single arXiv paper. These files land on all four, including two cases
// the corpus has no example of at all: a StructTreeRoot holding nothing usable, and a
// file that will not open.
func TestFixtureRouting(t *testing.T) {
	for _, tc := range []struct {
		file   string
		pages  int
		path   string
		images int
		why    string
	}{
		// Tagged: a real structure tree with paragraphs in it.
		{"adobe-samples/extractPdfInput.pdf", 3, "tagged", 1, "Adobe's own Extract sample, 105 paragraphs"},
		{"adobe-samples/watermark.pdf", 1, "tagged", 1, "small but genuinely tagged"},
		{"pymupdf/test-styled-table.pdf", 1, "tagged", 0, "tagged table, no headings"},

		// Layout: fonts but no usable structure. The ordinary born-digital case.
		{"pymupdf/2201.00069.pdf", 1, "layout", 0, "LaTeX Type1"},
		{"pymupdf/mupdf_explored.pdf", 285, "layout", 0, "285-page manual, Type1 + Type3"},
		{"pymupdf/chinese-tables.pdf", 1, "layout", 0, "CJK Type0"},
		{"pymupdf/type3font.pdf", 1, "layout", 0, "Type3 only"},
		{"adobe-samples/exportPDFInput.pdf", 4, "layout", 8, "TrueType + Type0"},
		// The producer-stub case, and the reason probe's check is not "does a
		// StructTreeRoot exist". This file has one, and /MarkInfo /Marked true, and two
		// Document elements holding nothing — no headings, no paragraphs, no MCIDs. A
		// pipeline that trusted the flag would take the tagged path and find no text to
		// join to. Adobe ships it as a sample.
		{"adobe-samples/sampleInvoice.pdf", 3, "layout", 5, "StructTreeRoot with no content: a stub"},
		// An invisible OCR layer is still a text layer, and routing on it is right: the
		// text is there to extract and extracting it is cheaper and better than
		// re-running OCR on the image beside it.
		{"pymupdf-utilities/ocr-ed.pdf", 1, "layout", 1, "OCR text layer, invisible but present"},
		{"pymupdf-utilities/PDF_XChange-OCRed.pdf", 1, "layout", 1, "a second engine's OCR layer"},

		// OCR: no fonts anywhere, so there is no text to extract at any price.
		{"pymupdf-utilities/scanned.pdf", 1, "ocr", 1, "a scan, before OCR"},
		{"adobe-samples/ocrInput.pdf", 4, "ocr", 4, "Adobe's OCR sample, 4 scanned pages"},
		{"pymupdf/img-regular.pdf", 1, "ocr", 1, "one image, no text"},
		// 151 pages, no fonts, and no images either — upstream's own disqualified input.
		// Nothing to extract and nothing to rasterize, which is a file the OCR path will
		// also decline. Naming it as OCR is still the honest answer: no fonts means no
		// text, and what happens next is Phase 4's problem.
		{"adobe-samples/disqualifiedScannedPages.pdf", 151, "ocr", 0, "no fonts, no images"},
		// The image is two Form XObjects deep, and the fonts were deleted with it. Before
		// probe recursed into forms this reported 0 images, which was the visible half of
		// a bug whose invisible half was every nested font in the corpus.
		{"pymupdf/test_delete_image.pdf", 1, "ocr", 1, "image nested two forms deep"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			path := fixture(t, tc.file)
			r := probeOne(path, false)
			if r.Err != "" {
				t.Fatalf("probe: %s", r.Err)
			}
			if r.Pages != tc.pages {
				t.Errorf("pages = %d, want %d", r.Pages, tc.pages)
			}
			if r.Path != tc.path {
				t.Errorf("path = %q, want %q (%s)", r.Path, tc.path, tc.why)
			}
			if r.Images != tc.images {
				t.Errorf("images = %d, want %d (%s)", r.Images, tc.images, tc.why)
			}
			// probe and the images verb must agree about the same file, and before the
			// form recursion they did not: probe walked only page-level /Resources while
			// image.Reader descended into forms. Two counts of one document that differ is
			// a wrong answer whichever is right, and asserting agreement rather than only
			// the literal number is what keeps them from drifting apart again.
			if got := len(readImages(t, path)); got != r.Images {
				t.Errorf("probe says %d images, image.Reader finds %d", r.Images, got)
			}
		})
	}
}

// A zero-byte file is rejected with a reason.
//
// Upstream ships it under invalidinputs/ deliberately, and it is the corpus's missing
// case: every file in docs/ opens. What is asserted is that the failure is a reported
// error rather than a panic, an empty success, or a nil-pointer walk through a document
// that was never parsed. "No panics on malformed input, ever" is a §9 quality gate, and
// zero bytes is the smallest malformed input there is.
func TestFixtureZeroLengthRejected(t *testing.T) {
	path := fixture(t, "adobe-samples/zeroLength.pdf")

	r := probeOne(path, false)
	if r.Err == "" {
		t.Fatal("probe reported no error for a zero-byte file")
	}
	if r.Path != "unknown" {
		t.Errorf("path = %q, want %q: nothing was parsed, so no route can be claimed", r.Path, "unknown")
	}
	t.Logf("rejected: %s", r.Err)

	// Every verb has to decline it, and decline it the same way. A command that
	// succeeded here would write an empty document that looks like a conversion.
	out := filepath.Join(t.TempDir(), "out")
	for name, run := range map[string]func([]string) error{
		"md":     runMD,
		"okf":    func(a []string) error { return runOKF(a) },
		"images": runImages,
	} {
		args := []string{"-o", out, path}
		if err := run(args); err == nil {
			t.Errorf("%s: no error for a zero-byte file", name)
		}
	}
}

// TestFixtureLigatureSplitsIntoCharacters is the "ecient" case, which is the failure
// docs/DESIGN.md §1 names first.
//
// One glyph is not one character: "ﬂ" has a code point, and "f_t" does not, which is why
// font resolution returns a string per glyph rather than a rune. Upstream built this file
// to hold the same word twice, once with a ligature and once without, so the two
// renderings are directly comparable in a way a document containing only one of them is
// not.
//
// The font holding the ligature is reachable only through two nested Form XObjects. That
// is what made this file the second witness for the form-recursion fix in probe: a
// page-level scan reports it as having only Helvetica.
func TestFixtureLigatureSplitsIntoCharacters(t *testing.T) {
	md := mdOut(t, fixture(t, "pymupdf/text-find-ligatures.pdf"))

	// Both spellings must be present and must read as "flag" either way. The ligature
	// may arrive as U+FB02 or as the two letters — both are correct, and which one
	// depends on the font's /ToUnicode — but "ag" is not.
	if n := strings.Count(md, "ag' have a ligature?"); n != 2 {
		t.Errorf("%d of the 2 sentences survived: %q", n, md)
	}
	if !strings.Contains(md, "'flag'") {
		t.Errorf("the unligatured spelling is wrong: %q", md)
	}
	if strings.Contains(md, "'ag'") {
		t.Errorf("the ligature dropped its letters entirely — this is the \"ecient\" defect: %q", md)
	}

	r := probeOne(fixture(t, "pymupdf/text-find-ligatures.pdf"), false)
	if len(r.Fonts) != 2 {
		t.Errorf("fonts = %v, want both: the second is two Form XObjects deep", r.Fonts)
	}
}

// TestFixtureBoldSurvivesToMarkdown: emphasis has to come from the font, because on the
// layout path there is nothing else to take it from.
//
// small-table.pdf sets its row labels in Helvetica-Bold and its numbers in Helvetica, and
// the extractor splits a span at exactly that boundary. What this pins is the pair: the
// label is emphasized, the numbers beside it are not, and the delimiter sits outside the
// space between them — "**Metals** 357" rather than "**Metals ** 357", which CommonMark
// renders as literal asterisks.
func TestFixtureBoldSurvivesToMarkdown(t *testing.T) {
	md := mdOut(t, fixture(t, "pymupdf/small-table.pdf"))
	for _, want := range []string{
		"**Noble gases** -269 -62 -170.5",
		"**Metals** 357 >5000 2755.9",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	// A bold run that swallowed the following space emits its delimiters as text.
	if strings.Contains(md, "** ") && !strings.Contains(md, "** -") && !strings.Contains(md, "** 3") {
		t.Errorf("a delimiter run is adjacent to whitespace: %q", md)
	}
}

// TestFixtureNoTextIsNotAnError covers the files that correctly produce nothing.
//
// Three of the pymupdf fixtures are a page with one image on it, and type3font.pdf draws
// two glyphs from a Type3 font whose /CharProcs are bitmaps with no /ToUnicode and no
// meaningful /Differences — "/0" and "/1" as glyph names carry no text. Empty output is
// the honest answer for all four. The assertion is that it arrives as empty output and
// not as an error, and that nothing invents characters out of the drawing operators.
func TestFixtureNoTextIsNotAnError(t *testing.T) {
	for _, f := range []string{
		"pymupdf/img-regular.pdf",
		"pymupdf/img-transparent.pdf",
		"pymupdf/test-rewrite-images.pdf",
		"pymupdf/type3font.pdf",
	} {
		t.Run(f, func(t *testing.T) {
			if md := strings.TrimSpace(mdOut(t, fixture(t, f))); md != "" {
				t.Errorf("text from a file that has none: %q", md)
			}
		})
	}
}

// TestFixtureCJKNeedsNoSpaceInference: the space-inference rule must not fire between
// Han characters.
//
// docs/DESIGN.md §"Solving the spaces problem properly" infers a word boundary from the
// gap between glyphs, and CJK is where that inference has to be silent — Chinese is set
// without inter-word spaces, so a rule tuned on Latin would put one wherever a line
// happens to wrap. The corpus contains no CJK at all, which is why this file is here: the
// rule was never exercised against the case that breaks it, and it was in fact broken.
//
// Upstream's file is a Chinese bond prospectus page. What it caught was appendLine
// inserting a space at every wrapped-line join unconditionally, which is right for Latin
// — the break is the only thing marking the word boundary — and wrong for a script where
// a line simply fills and wraps mid-word. Three of the words below are wrapped in the
// original, and each came out cut into pieces.
func TestFixtureCJKNeedsNoSpaceInference(t *testing.T) {
	md := mdOut(t, fixture(t, "pymupdf/chinese-tables.pdf"))
	if !strings.Contains(md, "第七章") {
		t.Fatalf("a heading from the page is missing, so this measures nothing: %q", first(md, 200))
	}

	// The words the line wraps fall inside. Asserted individually rather than as a ratio
	// because a ratio cannot distinguish a broken word from a table column boundary, and
	// the whole question here is which of the two a space is. The company name spans
	// three lines and was emitted as "中诚信国际信 用评级有限责 任公司".
	for _, word := range []string{
		"中诚信国际信用评级有限责任公司", // wraps across three lines
		"联合资信评估有限公司",      // wraps across two
		"主体信用评级",          // a table header, wrapped inside its cell
		"偿还债务的能力很强",       // wraps mid-clause
	} {
		if !strings.Contains(md, word) {
			t.Errorf("%q is broken by an inferred space: a line wrap inside a CJK word is not "+
				"a word boundary", word)
		}
	}

	// The count is a second, weaker check on the same thing, and it is a bound rather
	// than an equality because the remaining splits are real: 7 of them, every one a
	// clause number before its title ("第七章 企业资信状况") or a table header cell
	// ("序号 金融机构名称 授信总额"). Those are genuine boundaries and removing them would
	// be the opposite defect.
	han, split := 0, 0
	rs := []rune(md)
	for i, r := range rs {
		if !isHan(r) {
			continue
		}
		han++
		if i+2 < len(rs) && rs[i+1] == ' ' && isHan(rs[i+2]) {
			split++
		}
	}
	if han < 400 {
		t.Fatalf("%d Han characters extracted, want the page's ~448", han)
	}
	if split > 10 {
		t.Errorf("%d spaces between two Han characters, want <= 10: measured 7, all of them "+
			"a clause number or a table column, and 22 before the wrap rule read the script",
			split)
	}
	t.Logf("%d Han characters, %d inferred splits between two of them", han, split)
}

func isHan(r rune) bool {
	return r >= 0x4E00 && r <= 0x9FFF || r >= 0x3400 && r <= 0x4DBF
}

// TestFixtureBadFontsLoseGlyphsNotPages: a defective font must cost its own glyphs and
// nothing else.
//
// Upstream named this file for the property. Its font dictionaries are broken in ways a
// loader can either absorb or fail on, and the whole argument of docs/DESIGN.md §1 is
// that failing on them is what the existing libraries do wrong — a PDF tool that stops at
// the first bad font is useless for the documents that most need extracting. Substituting
// a wrong glyph is visible in the output ("佛佛" where the page says something else) and
// that is the acceptable half of the trade.
func TestFixtureBadFontsLoseGlyphsNotPages(t *testing.T) {
	md := mdOut(t, fixture(t, "pymupdf/has-bad-fonts.pdf"))
	if strings.TrimSpace(md) == "" {
		t.Fatal("a broken font cost the whole page")
	}
	// The digits and Latin runs come through regardless of which glyph names resolved,
	// so they are what can be asserted without asserting the substitution itself.
	for _, want := range []string{"4406961485", "TORONTO,CANADA", "20150.00"} {
		if !strings.Contains(strings.ReplaceAll(md, " ", ""), want) {
			t.Errorf("%q missing: text was lost, not just glyphs", want)
		}
	}
}

// TestFixtureCircularOutlineTerminates: a cyclic outline tree must not hang.
//
// The tag reader already refuses a cyclic structure tree, and this is the same shape in
// the other tree a PDF carries. Upstream built the file for it. A test that hangs is
// worse than one that fails, so this is a real assertion even though it looks like a
// smoke test: without cycle handling it never returns and the suite times out.
func TestFixtureCircularOutlineTerminates(t *testing.T) {
	r := probeOne(fixture(t, "pymupdf/circular-toc.pdf"), false)
	if r.Err != "" {
		t.Fatalf("probe: %s", r.Err)
	}
	if r.Pages != 2 {
		t.Errorf("pages = %d, want 2", r.Pages)
	}
	mdOut(t, fixture(t, "pymupdf/circular-toc.pdf"))
}

// TestFixtureImagesEncodeToValidFiles is the corpus image test run over a different
// population.
//
// The corpus has 224 images and not one CCITT, JBIG2, or JPX among them, so ADR 0004's
// codec table was measured on a single family of producers. These files come from
// MuPDF's and Adobe's own samples: a transparent PNG-style image with an /SMask, five
// masked logos on an invoice, a DCT scan, an image two Form XObjects deep. Each must
// encode to a file that decodes at the dimensions the dictionary declared.
func TestFixtureImagesEncodeToValidFiles(t *testing.T) {
	for _, f := range []string{
		"pymupdf/img-regular.pdf",
		"pymupdf/img-transparent.pdf",
		"pymupdf/test-rewrite-images.pdf",
		"pymupdf/test_delete_image.pdf",
		"pymupdf-utilities/scanned.pdf",
		"pymupdf-utilities/ocr-ed.pdf",
		"adobe-samples/sampleInvoice.pdf",
		"adobe-samples/exportPDFInput.pdf",
		"adobe-samples/ocrInput.pdf",
		"adobe-samples/watermark.pdf",
	} {
		t.Run(f, func(t *testing.T) {
			ims := readImages(t, fixture(t, f))
			if len(ims) == 0 {
				t.Fatal("no images found")
			}
			for _, im := range ims {
				var buf bytes.Buffer
				if err := pdfimage.Encode(&buf, im); err != nil {
					t.Errorf("%s p%d (%s %dx%d): %v", im.Name, im.Page, im.Codec, im.Width, im.Height, err)
					continue
				}
				switch im.Codec {
				case pdfimage.CodecJPEG:
					// The passthrough promise of ADR 0004: a DCT stream is written as
					// its own bytes and never re-encoded.
					if !bytes.Equal(buf.Bytes(), im.Data) {
						t.Errorf("%s p%d: JPEG re-encoded (%d in, %d out)",
							im.Name, im.Page, len(im.Data), buf.Len())
					}
				default:
					cfg, err := png.DecodeConfig(bytes.NewReader(buf.Bytes()))
					if err != nil {
						t.Errorf("%s p%d: the PNG does not decode: %v", im.Name, im.Page, err)
						continue
					}
					if cfg.Width != im.Width || cfg.Height != im.Height {
						t.Errorf("%s p%d: PNG is %dx%d, dictionary says %dx%d",
							im.Name, im.Page, cfg.Width, cfg.Height, im.Width, im.Height)
					}
				}
			}
		})
	}
}

// TestFixtureSoftMaskWithoutMatte is the case the corpus almost does not have.
//
// 136 of the corpus's 143 soft masks carry /Matte [0 0 0], so its /SMask population is
// really one producer's habit measured 143 times, and ADR 0004's premultiplication
// statement rests on it. These two files carry an /SMask with no /Matte at all — plain
// alpha, nothing premultiplied — which is the branch that must not report a base image
// as premultiplied when it is not. Getting that backwards would have the Phase 4
// compositor un-premultiply samples that were never multiplied, darkening every edge.
func TestFixtureSoftMaskWithoutMatte(t *testing.T) {
	for _, tc := range []struct {
		file  string
		masks int
	}{
		{"pymupdf/img-transparent.pdf", 1},
		{"adobe-samples/sampleInvoice.pdf", 5},
	} {
		t.Run(tc.file, func(t *testing.T) {
			masks := 0
			for _, im := range readImages(t, fixture(t, tc.file)) {
				if im.SMask == nil {
					continue
				}
				masks++
				if im.Premultiplied() {
					t.Errorf("%s p%d: reported premultiplied, but the mask carries no /Matte",
						im.Name, im.Page)
				}
				if im.SMask.Components != 1 && im.SMask.Codec == pdfimage.CodecRaw {
					t.Errorf("%s p%d: soft mask has %d components, want 1 (§11.6.5.3)",
						im.Name, im.Page, im.SMask.Components)
				}
			}
			if masks != tc.masks {
				t.Errorf("%d soft masks, want %d", masks, tc.masks)
			}
		})
	}
}

// TestFixtureTaggedTableHasStructure: a tagged table declares its own grid, and that is
// the only way a table is recoverable without heuristics.
//
// Both files are tables and the corpus's are not comparable: ISO 32000-2's tables are
// inside a document whose structure tree is 78,469 elements, so nothing about the table
// itself is separable from it. These are one table each, from two different producers,
// small enough that the row and cell counts can be checked by opening the file.
//
// docs/DESIGN.md §10 lists Markdown table emission as open, so what is asserted here is
// the input to it: TR, TD, and TH arrive with the right cardinality. When the sink learns
// to emit a grid, this is the fixture it will be written against.
func TestFixtureTaggedTableHasStructure(t *testing.T) {
	for _, tc := range []struct {
		file             string
		rows, data, head int
	}{
		{"pymupdf/test-styled-table.pdf", 5, 8, 7},
		{"adobe-samples/extractPdfInput.pdf", 16, 30, 18},
	} {
		t.Run(tc.file, func(t *testing.T) {
			s, err := pcstore.Open(fixture(t, tc.file))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()

			tr, err := tag.Read(s)
			if err != nil {
				t.Fatalf("tag.Read: %v", err)
			}
			if tr == nil {
				t.Fatal("no structure tree")
			}
			st := tr.Stats()
			if st.Tables == 0 {
				t.Fatal("no Table element")
			}
			if got := st.Roles["TR"]; got != tc.rows {
				t.Errorf("TR = %d, want %d", got, tc.rows)
			}
			if got := st.Roles["TD"]; got != tc.data {
				t.Errorf("TD = %d, want %d", got, tc.data)
			}
			if got := st.Roles["TH"]; got != tc.head {
				t.Errorf("TH = %d, want %d", got, tc.head)
			}
			// A cell with no MCID is a cell with no text, and a table of those is a
			// grid that cannot be filled in.
			if st.MCIDs == 0 {
				t.Error("no MCIDs: the cells cannot be joined to page text")
			}
		})
	}
}

// TestFixtureListsRecoverLevels: L / LI / LBody is how a tagged document says "list", and
// the Markdown sink turns it into "- ".
//
// The corpus does have lists, but Adobe's Extract sample is the file Adobe's own
// documentation describes the element taxonomy against — the same L / Li / Lbl / Lbody
// vocabulary — so it is the reference for the mapping rather than another instance of it.
func TestFixtureListsRecoverLevels(t *testing.T) {
	path := fixture(t, "adobe-samples/extractPdfInput.pdf")

	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	tr, err := tag.Read(s)
	if err != nil || tr == nil {
		t.Fatalf("tag.Read: %v", err)
	}
	st := tr.Stats()
	_ = s.Close()

	if st.Lists != 4 {
		t.Errorf("L = %d, want 4", st.Lists)
	}
	if got := st.Roles["LI"]; got != 38 {
		t.Errorf("LI = %d, want 38", got)
	}

	// Each LI has to reach the output as a list item, or the structure was read and
	// then thrown away.
	md := mdOut(t, path)
	items := 0
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "- ") {
			items++
		}
	}
	if items < st.Roles["LI"] {
		t.Errorf("%d list items emitted for %d LI elements", items, st.Roles["LI"])
	}
	t.Logf("%d L, %d LI, %d list items emitted", st.Lists, st.Roles["LI"], items)
}

// TestFixtureHeadingsFromTags: the tagged path's headings must reach the output as ATX
// headings.
//
// The corpus asserts this at 981 headings on a 1,023-page specification, which is a
// number nobody can check by opening the file. Five headings on a three-page sample can
// be checked by opening the file, and that is the whole reason this is here.
func TestFixtureHeadingsFromTags(t *testing.T) {
	md := mdOut(t, fixture(t, "adobe-samples/extractPdfInput.pdf"))
	heads := 0
	for _, line := range strings.Split(md, "\n") {
		n := 0
		for n < len(line) && line[n] == '#' {
			n++
		}
		if n > 0 && n < len(line) && line[n] == ' ' {
			heads++
		}
	}
	if heads == 0 {
		t.Fatalf("no headings emitted from a tree with 5 H elements:\n%s", first(md, 400))
	}
	t.Logf("%d headings emitted", heads)
}

// TestFixtureLargeDocumentStaysLinear is the throughput check on a document that is
// neither the specification nor a one-page sample.
//
// 285 pages of technical manual, Type1 and Type3 together, full of real code samples. It
// is also the negative control for the OCR-layer fix: if monospace detection were
// firing on visible text anywhere, a manual made of code listings is where it would
// happen, and it does not — nothing here is marked monospaced at all, so the change to
// suppress it for hidden text cost no genuine code span.
func TestFixtureLargeDocumentStaysLinear(t *testing.T) {
	md := mdOut(t, fixture(t, "pymupdf/mupdf_explored.pdf"))
	m := measure(md, 0)
	m.log(t, "mupdf_explored")

	// Measured 375,696 non-space characters over 285 pages.
	if m.nonSpace < 300_000 {
		t.Errorf("non-space chars = %d, want >= 300000 over 285 pages", m.nonSpace)
	}
	if m.spaceRatio < 10 {
		t.Errorf("spaces = %.2f%%, want >= 10%%", m.spaceRatio)
	}
	if m.longFrac > 1.0 {
		t.Errorf("words >25ch = %.2f%%, want <= 1%%", m.longFrac)
	}

	// The escape rate here is 6.03 per 1000 characters against the corpus's bar of 2,
	// and the difference is the document rather than the sink: 2,609 of the 2,751
	// backslashes precede an asterisk, and every one of those asterisks is a C pointer
	// declarator or a "/*" comment opener — "fz_context \*ctx", "const char \*uri".
	// Unescaped they open emphasis and swallow the text to the next one, so each is
	// load-bearing. Standards prose contains almost no asterisks, which is why the
	// corpus never measured this.
	//
	// So the bar is split. Everything except the asterisk is held to the corpus figure,
	// which is what catches the escaping getting broadly more aggressive; the asterisks
	// are bounded loosely, since their count is a property of C syntax.
	if r := escapeRate(md); r > 8 {
		t.Errorf("escape rate %.2f per 1000 chars, want <= 8", r)
	}
	if r := escapeRate(strings.ReplaceAll(md, `\*`, "")); r > 1 {
		t.Errorf("escape rate excluding pointer asterisks %.2f per 1000 chars, want <= 1", r)
	}
}

// TestFixtureNoControlBytes runs the CommonMark §2.3 substitution over the reference
// population.
//
// The corpus assertion of this found three NULs, all in one file, from a /ToUnicode entry
// mapping a code to U+0000 — and the corpus is gitignored, so on a clone without it that
// test asserts nothing. This one always runs.
func TestFixtureNoControlBytes(t *testing.T) {
	for _, f := range fixturePDFs(t) {
		t.Run(f, func(t *testing.T) {
			if f == "adobe-samples/zeroLength.pdf" {
				t.Skip("deliberately unopenable")
			}
			md := mdOut(t, fixture(t, f))
			for i := 0; i < len(md); i++ {
				c := md[i]
				if c == '\n' || c == '\r' || c == '\t' || c >= 0x20 && c != 0x7f {
					continue
				}
				t.Fatalf("control byte 0x%02x at offset %d, in %q", c, i, context(md, i))
			}
		})
	}
}

// TestFixtureDeterministic: two runs over the same file agree exactly.
//
// Map iteration order and a pointer into a reallocated slice both produce output that
// differs between runs, and this package has shipped both. The corpus version of this
// test runs on one file; these are 28 documents from four producers, which is where a
// map that happens to have one element stops hiding the defect.
func TestFixtureDeterministic(t *testing.T) {
	for _, f := range fixturePDFs(t) {
		if f == "adobe-samples/zeroLength.pdf" {
			continue
		}
		t.Run(f, func(t *testing.T) {
			path := fixture(t, f)
			if a, b := mdOut(t, path), mdOut(t, path); a != b {
				t.Error("two runs disagree")
			}
		})
	}
}

// fixturePDFs lists every reference PDF, relative to testdata/.
func fixturePDFs(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(fixtureDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			return nil
		}
		rel, rerr := filepath.Rel(fixtureDir, path)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no reference fixtures — run `pwsh testdata/fetch.ps1 -Download`")
	}
	return out
}

func readImages(t *testing.T, path string) []*pdfimage.Image {
	t.Helper()
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	ims, err := pdfimage.NewReader(s).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	return ims
}

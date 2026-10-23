package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/model-harness/pdftools/bench"
	"github.com/model-harness/pdftools/pdfspec"
)

// TestMain turns OCR and its second reader off for every test in the package, so the Tesseract
// and the inferd a machine has cannot change what the tests measure: md and bench use them when
// they are found (ADRs 0043 and 0044). The tests that want them name them.
func TestMain(m *testing.M) {
	// Run as the command itself, for a test that needs what main does with its arguments, such
	// as a usage message after which the flag package exits (pdfspecMain).
	if args, ok := os.LookupEnv("PDFSPEC_TEST_AS_MAIN"); ok {
		os.Args = append([]string{"pdfspec"}, strings.Fields(args)...)
		main()
		os.Exit(0)
	}
	for _, v := range []string{tesseractEnv, inferdEnv} {
		if err := os.Setenv(v, "off"); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

// testTesseract is the tesseract the opt-in tests run, or a skip.
func testTesseract(t *testing.T) {
	t.Helper()
	exe := os.Getenv("PDFSPEC_TEST_TESSERACT")
	if exe == "" {
		t.Skip("PDFSPEC_TEST_TESSERACT names no tesseract")
	}
	t.Setenv(tesseractEnv, exe)
}

// TestMDWithoutTesseractKeepsTheScan: a scanned page with no engine to read it is written as
// the extractor found it, the run succeeds, and stderr names the page.
func TestMDWithoutTesseractKeepsTheScan(t *testing.T) {
	in := fixture(t, "reference/scan-paragraphs.pdf")
	t.Setenv(tesseractEnv, "")
	t.Setenv("PATH", t.TempDir())
	out := filepath.Join(t.TempDir(), "out.md")
	warned := captureStderr(t, func() {
		if err := runMD([]string{"-o", out, in}); err != nil {
			t.Errorf("runMD: %v", err)
		}
	})
	if !strings.Contains(warned, "1 page(s) need OCR and tesseract not found") || !strings.Contains(warned, "extracted text: 1") {
		t.Errorf("stderr = %q, want the page that needs OCR named", warned)
	}

	// The variable names the engine when no flag does.
	t.Setenv(tesseractEnv, filepath.Join(t.TempDir(), "no-tesseract.exe"))
	if warned := captureStderr(t, func() { _ = runMD([]string{"-o", out, in}) }); !strings.Contains(warned, "no-tesseract.exe does not run") {
		t.Errorf("with %s naming a missing file, stderr = %q", tesseractEnv, warned)
	}

	// Off is off: nothing is said, since nothing was asked.
	t.Setenv(tesseractEnv, "off")
	if warned := captureStderr(t, func() { _ = runMD([]string{"-o", out, in}) }); strings.Contains(warned, "OCR") ||
		strings.Contains(warned, "tesseract") {
		t.Errorf("with OCR off, stderr = %q", warned)
	}
}

// TestMDRejectsABadMinConf: the shared engine flags are checked before anything runs.
func TestMDRejectsABadMinConf(t *testing.T) {
	in := fixture(t, "reference/scan-paragraphs.pdf")
	for _, v := range []string{"-1", "100.5"} {
		if err := runMD([]string{"-min-conf", v, in}); err == nil {
			t.Errorf("-min-conf %s accepted", v)
		}
	}
}

// TestBenchmarkWithTesseract is #9's done-when: with Tesseract present, the gold set's scanned
// pages meet the v1 character target, and no born-digital fixture moves, since OCR only adds
// to what the extractor found (ocr.Adds). Skipped without PDFSPEC_TEST_TESSERACT.
func TestBenchmarkWithTesseract(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "reference")
	off, err := benchRows(dir)
	if err != nil {
		t.Fatal(err)
	}
	testTesseract(t)
	on, err := benchRows(dir)
	if err != nil {
		t.Fatal(err)
	}
	cer, n := 0.0, 0
	for i, r := range on {
		switch {
		case strings.HasPrefix(r.name, "scan-"):
			cer += r.s.CER
			n++
		case r.name == "type3-unmapped":
			// The page of glyphs with no value is read whole, its 15 [err] with it.
			if r.s.CER > 0.01 || r.s.Errs != 0 {
				t.Errorf("type3-unmapped: CER %.3f with %d [err], want it read", r.s.CER, r.s.Errs)
			}
		case r.name == "type3-words":
			// The page keeps its text and its 42 glyphs with no value are read where the text
			// places them, 41 of them; the dollar set alone waits for a second reader (ADR 0048).
			if r.s.CER > 0.002 || r.s.Errs != 1 || off[i].s.Errs != 11 {
				t.Errorf("type3-words: CER %.3f with %d [err], from %d; want 1 left of 11", r.s.CER, r.s.Errs, off[i].s.Errs)
			}
		case r.name == "image-kinds":
			// The text and the table are read off their images; the chart is a model's to read,
			// and without one stays [image] (TestImageKindsAreRead).
			if r.s.CER > 0.1 || r.s.Tables < 0.7 {
				t.Errorf("image-kinds: CER %.3f, tables %.3f, want its text and table read", r.s.CER, r.s.Tables)
			}
		case r.s != off[i].s:
			t.Errorf("%s moved with Tesseract present: %+v, was %+v", r.name, r.s, off[i].s)
		}
	}
	if n != 4 {
		t.Fatalf("%d scanned fixtures, want 4", n)
	}
	if mean := cer / float64(n); mean > benchTargets.cer {
		t.Errorf("scanned pages' mean CER %.4f, above the v1 target %.2f", mean, benchTargets.cer)
	}
	// #12's done-when (ADR 0047): over the scans, every v1 target.
	var scans []benchRow
	for _, r := range on {
		if strings.HasPrefix(r.name, "scan-") {
			scans = append(scans, r)
		}
	}
	sm := benchMeans(scans)
	// A dimension no scan exercises, every one scoring its vacuous 1, has no mean to miss: the
	// four scans have no styled span among them.
	vacuous := map[string]bool{"headings": true, "tables": true, "style": true}
	for _, r := range scans {
		vacuous["headings"] = vacuous["headings"] && r.s.Headings == 1
		vacuous["tables"] = vacuous["tables"] && r.s.Tables == 1
		vacuous["style"] = vacuous["style"] && r.s.Style == 1
	}
	for _, miss := range benchMisses(sm, benchTargets) {
		if !vacuous[miss[:strings.Index(miss, ":")]] {
			t.Errorf("scans' %s", miss)
		}
	}
	// With an engine for pixels, every fixture counts, and every target holds over all.
	m := benchMeans(on)
	t.Logf("means over every fixture with Tesseract: %+v", m)
	for _, miss := range benchMisses(m, benchTargets) {
		t.Errorf("over every fixture with Tesseract, %s", miss)
	}
}

// TestImageKindsAreRead is #11's done-when on the gold set's page of images (ADR 0046): with
// Tesseract the text image is text and the table image a table, each at its character and cell
// target; with inferd's vision model too, the chart is a chart, its values a table, and the
// picture a picture. Skipped without the engines.
func TestImageKindsAreRead(t *testing.T) {
	testTesseract(t)
	path := fixture(t, "reference/image-kinds.pdf")
	withModel := os.Getenv("PDFSPEC_TEST_INFERD") != ""
	if withModel {
		t.Setenv(inferdEnv, "auto")
	}
	opt := defaultEngines().options()
	opt.Log = io.Discard
	pd, err := pdfspec.Open(path, opt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pd.Close() }()
	if err := pd.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := pd.Doc
	var kinds []string
	for _, im := range d.Pages[0].Images {
		k := "unread"
		if im.Read != nil {
			k = im.Read.Kind
		}
		kinds = append(kinds, k)
	}
	want := "text table unread unread"
	if withModel {
		want = "text table chart picture"
	}
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("kinds = %s, want %s", got, want)
	}
	gold, err := os.ReadFile(fixture(t, "reference/image-kinds.gold.md")) // #nosec G304 -- a committed fixture
	if err != nil {
		t.Fatal(err)
	}
	md, err := convertDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	sc := bench.Score(gold, md)
	if withModel && (sc.CER > benchTargets.cer || sc.Tables < benchTargets.tables) {
		t.Errorf("with the model: CER %.3f tables %.3f, want the v1 targets", sc.CER, sc.Tables)
	}
}

// TestOCRVerbWithoutTesseractSucceeds: the verb, like md, keeps a scan's extracted text when no
// engine is installed, and exits 0.
func TestOCRVerbWithoutTesseractSucceeds(t *testing.T) {
	t.Setenv(tesseractEnv, "")
	t.Setenv("PATH", t.TempDir())
	out := filepath.Join(t.TempDir(), "out.md")
	if err := runOCR([]string{"-o", out, fixture(t, "reference/scan-paragraphs.pdf")}); err != nil {
		t.Errorf("runOCR without tesseract: %v", err)
	}
}

// TestBenchReadsWithOCR: bench scores what md writes, so it asks for OCR as md does, here with
// an engine that is not there.
func TestBenchReadsWithOCR(t *testing.T) {
	t.Setenv(tesseractEnv, filepath.Join(t.TempDir(), "no-tesseract.exe"))
	warned := captureStderr(t, func() {
		if _, err := convertDefault(fixture(t, "reference/scan-paragraphs.pdf")); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(warned, "no-tesseract.exe does not run") {
		t.Errorf("stderr = %q, want bench to have asked for OCR", warned)
	}
}

// TestEngineFlagsFallBackToTheEnvironment: a flag names the engine; without one the environment
// does, and inferd's "auto" is the platform's address.
func TestEngineFlagsFallBackToTheEnvironment(t *testing.T) {
	t.Setenv(tesseractEnv, "env-tesseract")
	t.Setenv(inferdEnv, "off")
	if exe, addr := envEngines("", ""); exe != "env-tesseract" || addr != "off" {
		t.Errorf("from the environment: %q %q", exe, addr)
	}
	if exe, addr := envEngines("flag-tesseract", "off"); exe != "flag-tesseract" || addr != "off" {
		t.Errorf("from the flags: %q %q", exe, addr)
	}
	t.Setenv(inferdEnv, "auto")
	if _, addr := envEngines("", ""); addr != "" {
		t.Errorf("auto: %q, want the platform's address", addr)
	}
	if o := (engineFlags{inferd: "off"}).options(); o.Inferd != "off" || o.Log != os.Stderr {
		t.Errorf("options: inferd %q, log %v", o.Inferd, o.Log)
	}
}

// TestBundledTesseractIsFoundBesideTheCommand: a release's Tesseract is found where its archive
// unpacks it, beside pdfspec, and where install.sh puts it, ../share/pdfspec from the command's
// directory, with its own model; a directory with neither has none.
func TestBundledTesseractIsFoundBesideTheCommand(t *testing.T) {
	name := "tesseract"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	place := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "tessdata"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unpacked := t.TempDir()
	place(t, filepath.Join(unpacked, "tesseract"))
	installed := t.TempDir()
	place(t, filepath.Join(installed, "share", "pdfspec", "tesseract"))
	for _, c := range []struct {
		name, dir, want string
	}{
		{"unpacked", unpacked, filepath.Join(unpacked, "tesseract")},
		{"installed", filepath.Join(installed, "bin"), filepath.Join(installed, "share", "pdfspec", "tesseract")},
		{"none", t.TempDir(), ""},
	} {
		exe, data := bundledTesseract(c.dir)
		switch {
		case c.want == "" && (exe != "" || data != ""):
			t.Errorf("%s: %q %q, want none", c.name, exe, data)
		case c.want != "" && (exe != filepath.Join(c.want, name) || data != filepath.Join(c.want, "tessdata")):
			t.Errorf("%s: %q %q, want the one in %s", c.name, exe, data, c.want)
		}
	}
}

// TestBenchmarkWithBothEngines is the gold set with Tesseract and inferd's vision model, as md
// runs with both installed: every v1 target over all fixtures, no fixture's characters worse than
// with Tesseract alone, and no more [err]. TestImageKindsAreRead checks what the model reads off
// each kind of image. Skipped unless
// PDFSPEC_TEST_TESSERACT and PDFSPEC_TEST_INFERD both name an engine.
func TestBenchmarkWithBothEngines(t *testing.T) {
	if os.Getenv("PDFSPEC_TEST_INFERD") == "" {
		t.Skip("PDFSPEC_TEST_INFERD is not set")
	}
	dir := filepath.Join("..", "..", "testdata", "reference")
	testTesseract(t)
	alone, err := benchRows(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(inferdEnv, "auto")
	both, err := benchRows(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, a := benchMeans(both), benchMeans(alone)
	t.Logf("means over every fixture with both engines: %+v", m)
	for _, miss := range benchMisses(m, benchTargets) {
		t.Errorf("over every fixture with both engines, %s", miss)
	}
	for i, r := range both {
		if r.s.CER > alone[i].s.CER+0.001 {
			t.Errorf("%s: CER %.4f with the model, %.4f without", r.name, r.s.CER, alone[i].s.CER)
		}
	}
	if m.errs > a.errs {
		t.Errorf("%.3f [err] per fixture with the model, %.3f without", m.errs, a.errs)
	}
}

// TestInferdTimeoutFromTheEnvironment: PDFSPEC_INFERD_TIMEOUT is a Go duration or bare seconds;
// "0" waits as long as the model takes; empty, malformed or negative is the library's default,
// since a typo in an environment variable must not break the conversion.
func TestInferdTimeoutFromTheEnvironment(t *testing.T) {
	for _, c := range []struct {
		env  string
		want time.Duration
	}{
		{"", 0},
		{"30s", 30 * time.Second},
		{"2m", 2 * time.Minute},
		{"45", 45 * time.Second},
		{"0", -1},
		{"garbage", 0},
		{"-5s", 0},
	} {
		t.Setenv(inferdTimeoutEnv, c.env)
		if got := defaultEngines().options().InferdTimeout; got != c.want {
			t.Errorf("%s=%q: %v, want %v", inferdTimeoutEnv, c.env, got, c.want)
		}
	}
}

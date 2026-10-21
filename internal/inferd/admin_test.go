package inferd

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// adminOpening is what inferd 0.6 writes on connect, from `inferdctl status` on this machine:
// an embedding backend, a generation backend with vision, then the snapshot.
const adminOpening = `{"accelerator":"cuda","audio":false,"backend":"embeddinggemma-300m","embed":true,"id":"admin","status":"capabilities","type":"status","v2":false,"vision":false,"wire_version":1}
{"accelerator":"cuda","audio":true,"backend":"gemma-4-e4b","embed":false,"id":"admin","status":"capabilities","thinking":true,"type":"status","v2":true,"vision":true,"wire_version":1}
{"id":"admin","status":"ready","type":"status"}
`

func TestReadStatusFindsTheVisionBackend(t *testing.T) {
	st, err := readStatus(strings.NewReader(adminOpening))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Ready() || len(st.Capabilities) != 2 {
		t.Fatalf("status = %+v", st)
	}
	c, ok := st.Vision()
	if !ok || c.Backend != "gemma-4-e4b" {
		t.Errorf("Vision = %+v, %v", c, ok)
	}
}

func TestReadStatusWithoutVision(t *testing.T) {
	for name, in := range map[string]string{
		// An embedding model sees no images, and neither does a v1 backend that says it does.
		"embedding only": `{"backend":"e","status":"capabilities","v2":false,"vision":false}` + "\n" + `{"status":"ready"}` + "\n",
		"vision on v1":   `{"backend":"old","status":"capabilities","v2":false,"vision":true}` + "\n" + `{"status":"ready"}` + "\n",
		"key absent":     `{"backend":"g","status":"capabilities","v2":true}` + "\n" + `{"status":"ready"}` + "\n",
	} {
		st, err := readStatus(strings.NewReader(in))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := st.Vision(); ok {
			t.Errorf("%s: a backend with vision was found", name)
		}
	}
}

func TestReadStatusWhileLoading(t *testing.T) {
	st, err := readStatus(strings.NewReader(`{"status":"loading_model","phase":"x"}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Ready() || st.State != "loading_model" {
		t.Errorf("status = %+v", st)
	}
}

func TestReadStatusRejects(t *testing.T) {
	cap := `{"backend":"g","status":"capabilities","v2":true,"vision":true}` + "\n"
	for name, in := range map[string]string{
		"empty":                 "",
		"not JSON":              "hello\n",
		"no snapshot":           cap,
		"no newline, no status": strings.TrimSuffix(cap, "\n"),
		"endless capabilities":  strings.Repeat(cap, maxAdminFrames+1),
	} {
		if _, err := readStatus(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A snapshot with no trailing newline is still read.
	if st, err := readStatus(strings.NewReader(`{"status":"ready"}`)); err != nil || !st.Ready() {
		t.Errorf("unterminated snapshot: %+v, %v", st, err)
	}
}

// TestReadStatusWithNoDaemon: a socket nobody serves is "not ready", the caller's signal to go on
// without the model.
func TestReadStatusWithNoDaemon(t *testing.T) {
	addr := filepath.Join(t.TempDir(), "admin.sock")
	if runtime.GOOS == "windows" {
		addr = `\\.\pipe\pdfspec-test-no-such-inferd-admin`
	}
	start := time.Now()
	if _, err := ReadStatus(context.Background(), addr); !errors.Is(err, ErrBackendNotReady) {
		t.Errorf("ReadStatus = %v, want ErrBackendNotReady", err)
	}
	// A run without inferd asks once a document, and learns there is none within the dial bound.
	if d := time.Since(start); d > time.Second {
		t.Errorf("learning there is no daemon took %v", d)
	}
}

// TestImagesFirstOrdersTheBlocks: Google's examples, and the word-reading spike, put the image
// before the question.
func TestImagesFirstOrdersTheBlocks(t *testing.T) {
	for _, c := range []struct {
		first bool
		want  string
	}{
		{false, "text,image"},
		{true, "image,text"},
	} {
		b, err := json.Marshal(Message{Role: RoleUser, Content: "Read it.", ImageRefs: []string{"a"}, ImagesFirst: c.first})
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Content []struct{ Type string } `json:"content"`
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, bl := range m.Content {
			got = append(got, bl.Type)
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("ImagesFirst %v: blocks %v, want %s", c.first, got, c.want)
		}
	}
}

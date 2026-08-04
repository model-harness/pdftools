// Package docd hosts granite-docling behind the ocr/ipc wire.
//
// It is the lightweight stand-in for a real inference daemon, and being a stand-in is
// the design. inferd already speaks this protocol and already embeds llama.cpp, so it
// is where this belongs — but it loads one warm model per process and that model is
// not granite-docling today. Rather than block the OCR path on someone else's
// roadmap, docd serves the identical wire: when inferd carries docling, the CLI points
// at its socket instead and nothing else changes, because neither side can tell the
// difference. That is what the byte-compatibility in ocr/ipc buys, and this package is
// the thing it buys it from.
//
// The shape is three moving parts and no cleverness:
//
//	ocr verb  --ipc-->  docd  --http-->  llama-server  --->  granite-docling
//
// docd owns the model's lifetime and the protocol translation. It does not own the
// inference: llama.cpp does that, because a VLM runtime is not a thing to reimplement
// for this.
//
// # Why a subprocess and not a library
//
// llama.cpp is C++, so linking it means CGO, and CGO means the single static
// cross-compiling binary this repo values stops existing. A subprocess keeps that: the
// CLI stays pure Go and stays small, the GPU-linked code lives in a process that can
// be missing entirely, and a model that segfaults on a malformed page kills a child
// rather than the run. The cost is a process boundary and an HTTP hop, which against
// tens of seconds of generation per page does not register.
//
// # The executable is found, never downloaded
//
// docd locates llama-server on PATH and reports how to install it when absent. It does
// not fetch it. Downloading and executing a binary is a supply-chain step of a
// different kind than downloading data, and it is not one a PDF tool should take on
// its user's behalf silently.
//
// Model weights are different, and llama.cpp's own -hf flag fetches them into its
// cache with its own integrity checks. That is deliberately not reimplemented here:
// a second downloader would mean a second cache, a second checksum policy, and two
// places for a partially written GGUF to hide.
package docd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Model is the HuggingFace repo llama.cpp downloads when none is configured.
//
// granite-docling-258M rather than a larger docling model, for reasons that are all
// about this being the default: it is around an eighth the size, IBM publishes an
// official GGUF with a matching mmproj so the vision tower is not a separate hunt, the
// base weights are Apache-2.0, and it emits DocTags — structured output the parser
// reads directly, rather than prose that would have to be re-analysed. A repo whose
// license is MIT cannot make a copyleft model its default, so the license is a gate
// here, not a preference.
const Model = "ibm-granite/granite-docling-258M-GGUF"

// Options configure a host.
type Options struct {
	// Model is the -hf repo. Empty means Model.
	Model string

	// Exe is the llama-server executable. Empty means look on PATH.
	Exe string

	// Port is llama-server's HTTP port. Zero means DefaultPort.
	//
	// Bound on loopback only, and there is no option to change that: this is a
	// process-private channel between two halves of one tool, and an inference
	// endpoint reachable off-box is an unauthenticated GPU for anyone who finds it.
	Port int

	// GPULayers is -ngl. Zero means CPU only, which is the honest default — a
	// machine without a usable GPU would otherwise fail at model load with an error
	// from a layer of the stack that cannot explain itself.
	GPULayers int

	// Ctx is the context window, -c. Zero means llama-server's default.
	Ctx int

	// Ready bounds the wait for the model to load. Zero means DefaultReady.
	Ready time.Duration

	Log *slog.Logger
}

// Defaults. The readiness bound is generous because it covers a first run, where
// llama.cpp is downloading ~500 MB of GGUF and mmproj before it can load anything;
// a subsequent start is seconds. Failing at 30 s would make the first run of the tool
// look broken exactly once, which is the worst time for it.
const (
	DefaultPort  = 18080
	DefaultReady = 10 * time.Minute
)

// Host is a running llama-server plus the translation from this repo's wire to its
// HTTP API. It satisfies ipc.Handler.
type Host struct {
	cmd  *exec.Cmd
	url  string
	http *http.Client
	log  *slog.Logger
	name string
}

// Start launches llama-server and waits until its model is loaded.
//
// Returns only when the server reports ready, so a caller can bind its own socket
// immediately afterwards and have a successful connect mean the model is warm. That
// ordering is the protocol's readiness signal — there is no separate health frame —
// so getting it backwards would make every client's first request fail instead of
// wait.
func Start(ctx context.Context, o Options) (*Host, error) {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	model := o.Model
	if model == "" {
		model = Model
	}
	port := o.Port
	if port == 0 {
		port = DefaultPort
	}
	ready := o.Ready
	if ready == 0 {
		ready = DefaultReady
	}

	exe, err := locate(o.Exe)
	if err != nil {
		return nil, err
	}

	args := []string{
		"-hf", model,
		// --mmproj-auto is llama.cpp's default when -hf is used, stated explicitly
		// because the vision tower is the difference between this working and the
		// server loading the text model and rejecting every image.
		"--mmproj-auto",
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(port),
		"-ngl", fmt.Sprint(o.GPULayers),
		// One slot, one sequence. A page is a full-resolution image and the context is
		// mostly image tokens, so parallel slots divide the window rather than
		// multiplying throughput. Concurrency belongs at the page level, above this.
		"--parallel", "1",
		// Greedy decoding. Transcription has one right answer, and sampling variation
		// on a transcription task means two runs over one document disagree about what
		// it says.
		"--temp", "0",
	}
	if o.Ctx > 0 {
		args = append(args, "-c", fmt.Sprint(o.Ctx))
	}

	// Running a named executable is this package's entire purpose, and the name is not
	// attacker-controlled in any sense that a fixed path would fix: it comes from -exe or
	// from exec.LookPath, so a caller who can set it can already run anything. No shell is
	// involved — exec.Command takes an argv, so nothing in args is interpreted — and the
	// only value that reaches the command line from outside is the model repo, which
	// llama.cpp validates itself.
	// #nosec G204 -- launching a located llama-server is the API; argv, no shell
	cmd := exec.CommandContext(ctx, exe, args...)
	// llama-server writes progress and load diagnostics to stderr. Forwarded rather
	// than swallowed: on a first run this is the only indication that a 500 MB
	// download is in progress, and a silent ten-minute wait is indistinguishable from
	// a hang.
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	// Kill the whole child on context cancellation rather than leaving an orphan
	// holding the GPU and the port.
	cmd.Cancel = func() error { return cmd.Process.Kill() }

	log.Info("starting llama-server", "exe", exe, "model", model, "port", port, "ngl", o.GPULayers)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("docd: start %s: %w", exe, err)
	}

	h := &Host{
		cmd: cmd,
		url: fmt.Sprintf("http://127.0.0.1:%d", port),
		// No client timeout. A page's generation legitimately runs for minutes on CPU,
		// and a timeout here would cut it off mid-DocTags with no way to tell that from
		// a hung server. Cancellation is the caller's context, which is bounded by the
		// verb.
		http: &http.Client{},
		log:  log,
		name: "llama.cpp/" + model,
	}

	if err := h.wait(ctx, ready); err != nil {
		_ = h.Close()
		return nil, err
	}
	log.Info("model ready", "model", model)
	return h, nil
}

// locate resolves the llama-server executable.
func locate(exe string) (string, error) {
	if exe != "" {
		if _, err := os.Stat(exe); err != nil {
			return "", fmt.Errorf("docd: %s: %w", exe, err)
		}
		return exe, nil
	}
	for _, name := range []string{"llama-server", "llama-server.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("docd: llama-server not found on PATH.\n%s", install())
}

// install returns the platform's install instruction.
//
// Named commands rather than a URL, because the alternative to a working instruction
// here is the user finding a random binary, and this is the one place in the tool that
// can influence which. Official channels only, and no auto-download: fetching and
// running an executable on someone's behalf is not a step a PDF tool takes quietly.
func install() string {
	switch runtime.GOOS {
	case "windows":
		return "Install with `winget install llama.cpp` or download an official release " +
			"from https://github.com/ggml-org/llama.cpp/releases and put llama-server.exe on PATH.\n" +
			"Point at an existing build with -exe, or at an already-running host with -addr."
	case "darwin":
		return "Install with `brew install llama.cpp`.\n" +
			"Point at an existing build with -exe, or at an already-running host with -addr."
	default:
		return "Build from https://github.com/ggml-org/llama.cpp (`cmake -B build && cmake --build build`) " +
			"and put llama-server on PATH.\n" +
			"Point at an existing build with -exe, or at an already-running host with -addr."
	}
}

// wait polls /health until the model is loaded.
//
// /health rather than a TCP connect, because llama-server binds its port before the
// model is loaded and answers 503 "Loading model" in the meantime. A readiness check
// that only proved the port was open would let the first page's request through to a
// server with no weights, which fails as a confusing 503 mid-run instead of as a wait.
func (h *Host) wait(ctx context.Context, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		if h.exited() {
			return errors.New("docd: llama-server exited during startup; see its output above")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/health", nil)
		if err != nil {
			return err
		}
		resp, err := h.http.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("docd: llama-server was not ready within %s", limit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (h *Host) exited() bool {
	return h.cmd.ProcessState != nil && h.cmd.ProcessState.Exited()
}

// Name identifies the backend on the wire's done frame.
func (h *Host) Name() string { return h.name }

// Close stops llama-server.
func (h *Host) Close() error {
	if h.cmd.Process == nil {
		return nil
	}
	_ = h.cmd.Process.Kill()
	// Reaped so the child does not linger as a zombie holding its port, which would
	// make an immediate restart fail on bind for reasons unrelated to the restart.
	_, _ = h.cmd.Process.Wait()
	return nil
}

// ---------------------------------------------------------------------------
// Generation
// ---------------------------------------------------------------------------

// Generate converts one page, streaming DocTags to emit as they arrive.
//
// Uses /v1/chat/completions with a data-URI image rather than /completion with
// multimodal_data, and that choice is not stylistic. /completion requires the caller
// to place llama.cpp's media marker in the prompt itself, and that marker is
// randomized per server process — get_media_marker in server-common.cpp returns
// "<__media_" + random + "__>" unless LLAMA_MEDIA_MARKER is set, while mtmd's own
// mtmd_default_marker still returns the documented "<__media__>". The two disagree, so
// a client hardcoding the documented value fails against a real server. The chat
// endpoint substitutes the live marker itself, which removes the whole problem instead
// of working around it.
func (h *Host) Generate(ctx context.Context, img *image.RGBA, prompt string, maxTokens int, emit func(string)) error {
	uri, err := dataURI(img)
	if err != nil {
		return err
	}

	body := map[string]any{
		"messages": []any{map[string]any{
			"role": "user",
			// Image first, then the instruction: granite-docling is instruction-tuned
			// with the page ahead of the task, and a model asked to convert a page it
			// has not been shown yet answers differently.
			"content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": uri}},
				map[string]any{"type": "text", "text": prompt},
			},
		}},
		"stream":      true,
		"temperature": 0,
	}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("docd: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.http.Do(req)
	if err != nil {
		if h.exited() {
			return errors.New("docd: llama-server exited; see its output above")
		}
		return fmt.Errorf("docd: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("docd: llama-server returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	return stream(resp.Body, emit)
}

// stream reads server-sent events and hands each content delta to emit.
func stream(r io.Reader, emit func(string)) error {
	sc := bufio.NewScanner(r)
	// A single SSE line holds one token's worth of JSON normally, but a server that
	// batches or a model that emits a long DocTags run in one chunk can exceed
	// bufio's 64 KiB default, and the symptom of that is a truncated page with no
	// error at all.
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)

	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue // event/id/retry lines and the blank separators
		}
		if data == "[DONE]" {
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// A malformed chunk is skipped rather than fatal. The stream is otherwise
			// intact and the page's remaining tokens are still worth having; failing
			// here would discard a nearly complete page over one bad line.
			continue
		}
		if chunk.Error != nil {
			return fmt.Errorf("docd: %s", chunk.Error.Message)
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				emit(c.Delta.Content)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("docd: reading stream: %w", err)
	}
	// The stream ended without [DONE]. Reported, because the difference between a
	// complete page and a connection that dropped at 90% is invisible in the output —
	// DocTags parses either.
	return errors.New("docd: stream ended without a completion marker; the page may be truncated")
}

// dataURI encodes the page as a base64 PNG data URI.
//
// PNG rather than JPEG. The payload is a page of text destined for an OCR model, and
// JPEG's ringing artifacts land exactly on the high-contrast glyph edges the model
// reads — trading bytes on a loopback socket for accuracy on the only thing the tool
// produces. The base64 cost is a third more bytes over localhost, which is not the
// constraint here.
func dataURI(img *image.RGBA) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("data:image/png;base64,")
	enc := base64.NewEncoder(base64.StdEncoding, &buf)
	// BestSpeed: this PNG exists for one hop over loopback and is never stored, so
	// compression ratio buys nothing and the encode is on the per-page hot path.
	pe := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := pe.Encode(enc, img); err != nil {
		return "", fmt.Errorf("docd: encode page: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("docd: encode page: %w", err)
	}
	return buf.String(), nil
}

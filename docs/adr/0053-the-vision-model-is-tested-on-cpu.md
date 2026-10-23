# 53. The vision model is tested on CPU

Date: 2026-10-23

## Status

Accepted.

## Context

inferd's vision model has three jobs. It describes pictures and charts and reads a chart's values (ADR 0046). It gives a second opinion on words Tesseract doubts (ADR 0044). And it agrees to a glyph set alone before that glyph replaces an `[err]` (ADR 0048).

None of it ran in CI, which has no daemon. Unit tests use fakes, and the live tests skip without `PDFSPEC_TEST_INFERD`. So the path was proven only on a machine with inferd, and the gold set's figures were only ever measured with the model off.

ADR 0052 made inferd an opt-in at install. That is code a user runs once, on a system nobody here tested.

## Decision

**The `inferd` workflow** (`.github/workflows/inferd.yml`) runs on `ubuntu-24.04`, on CPU, and installs inferd the way a user does:

- `install.sh` with `PDFSPEC_WITH_INFERD=1`.
- A runner's shell has no systemd user session, so the installer prints the daemon's start command instead, with the arguments its unit would pass, and the job runs that line as printed. Where there is a session, it checks the service the installer started.
- It waits until `probe` names the model, and fails at once if the daemon exits.
- It then runs the tests that ask the model:
  - `TestLiveVision` and `TestLiveDescribe` on the wire;
  - `TestImageKindsAreRead` on the gold set's page of images;
  - `TestBenchmarkWithBothEngines`, the whole gold set with Tesseract and the model.

  Each is required by name, and the job fails on a skip message, as the Tesseract job does.
- The model is cached on the inferd release pdfspec pins, and the Tesseract on the cache `build.yml` fills.
- It runs on a push that touches the model's client, OCR, the converter, layout, the fixtures or the installer. It also runs weekly, and by hand.

**`TestBenchmarkWithBothEngines`** holds every v1 target over all 33 fixtures with both engines. It also requires that no fixture's CER is worse than with Tesseract alone, and that no more `[err]` are left. Its first run found a gold that said less than its page (ADR 0052).

**Two things the spike found before the job could work:**

- **glibc.** inferd v0.8.0's Linux build needs glibc 2.38. On Debian 12, with 2.36, the daemon cannot start, failing on `GLIBC_2.38 not found`. So the installer checks the version before downloading, and says why it is skipping. The job runs on 24.04, with 2.39.
- **The daemon takes its sockets as arguments.** Started without its systemd unit, `inferd-daemon` exits asking for `--lock`. The installer's message gives the whole command, with `--lock`, `--uds` and `--admin-addr` under `$XDG_RUNTIME_DIR/inferd`, which is where pdfspec looks.

**Two more that the first CI runs found.**

- **The service ran a daemon that was not there.** A runner has a systemd user session, so the installer enabled inferd's unit. Its `ExecStart` names `%h/.local/bin/inferd-daemon`, while the daemon went to `PDFSPEC_INSTALL_DIR`. The installer now writes the unit with the path it used, and checks it did.

**The request timeout.** Each request to the model was bounded at 60 s (ADR 0044). That is for a daemon that never answers, and was set from the spike on the development machine, where the slowest strip took 33 s. On the 4-vCPU runner, with no accelerator, one description of a page image took 58.6 s.

- One run lost a picture to a request that ran over, and `TestImageKindsAreRead` failed. The next run passed. So any user reading on an ordinary CPU would have had pictures go undescribed at random.
- The bound is now five minutes, `inferd.DefaultTimeout`, which is several times a slower machine than that runner, and still bounded. `TestDefaultTimeoutOutlastsACPURequest` holds it above four times the measured request.
- The timeout was also read from `PDFSPEC_INFERD_TIMEOUT` inside `internal/inferd`. That broke ADR 0051's rule that the library reads no environment variable. It is `Options.InferdTimeout` now, and the command maps the variable onto it as it maps the others: a duration or bare seconds, with `0` for no bound.

## Acceptance

The spike ran in a clean `ubuntu:24.04` container with no GPU, from a mock release:

- The installer installed pdfspec and inferd in 33 s, and printed the start command.
- Started as printed, the daemon chose its CPU backend, `libggml-cpu-alderlake.so`. The model was ready in 195 s, about 6 GB downloaded.
- `probe` reported `tesseract 5.5.3 eng; gemma-4-e4b via inferd`. pdfspec read the page of images in 17 s on the container's 32 CPUs, as text, table, chart with its values, and picture.

With both engines, on this machine, `TestBenchmarkWithBothEngines` gives CER 0.0006, recall 0.998, precision 0.998, order 1.000, blocks 0.970, headings 0.978, tables 0.989 and style 0.967.

## Consequences

- Image descriptions, the second reader, and the installer's inferd path are tested on every change to them, and weekly against model drift in the pinned release.
- A runner has 4 vCPUs to the spike's 32. On it, with the model cached, the tests took 4 minutes:
  - `TestLiveVision` 16.5 s and `TestLiveDescribe` 58.6 s;
  - `TestImageKindsAreRead` 124 s;
  - `TestBenchmarkWithBothEngines` 108 s, with the same means in every digit as on the development machine.
- Moving to a new inferd release means changing the pin in both installers and the cache key here.

# fuse-stream-mvp (Go + Wails + FUSE)

Drag-to-upload for large videos without pre-downloading: a read-only virtual filesystem that streams from **Xvid MediaHub** over HTTP Range while the browser uploads.

## Current Status: M3 (macOS-only MVP)

**M3 is macOS-only** with production-ready features:
- **macOS 15.4+** with **macFUSE FSKit backend** (no kext)
- Robust Cocoa drag-and-drop (idempotent, validated)
- Automatic mount recovery (stale mount detection and cleanup)
- Sleep prevention during active transfers (IOPMAssertion)
- Dual-ref eviction system (tileRef + openRef)
- Self-hosted macOS CI build

Linux support (M1/M2) has been removed in this milestone. Future cross-platform support may be reconsidered post-M3.

---

## High-level flow

1) **Auth**: exchange `client_id`/`client_secret` (from a local config/env) for a **short‑lived OAuth token** (cached **in memory only**).
2) **List titles**: `GET /v1/jobs?expand=file` returns the user’s video titles and embedded file objects (ids, sizes, content types).
3) **User selection**: pick a job; if multiple outputs exist, let user pick the desired output (file id).
4) **Recipient**: prompt for a **recipient identifier** (free text), which becomes `autograph_tag`.
5) **Temp URL**: call `GET /v1/files/downloads` with `file_id`, `autograph_tag`, `redirect=true`. Follow the 30x redirect to the final URL.
6) **Mount**: expose a read-only FUSE filesystem with a `Staged/` directory. Each staged selection appears as a single fixed-size file under a temp subfolder (e.g. file_id+recipient_id).
7) **Drag**: the UI shows a big **draggable tile** that drags the **real file path** to the browser’s upload zone.
8) **Overlap**: as the browser reads the path, the app streams from the temp URL using HTTP Range. Wall time ≈ `max(download, upload)`.
9) **Evict**: after the upload (file handle closed), evict any local cache and remove the staged entry (configurable).

> Important: do **not** use “promised files” for drag. The drag must provide a **real file path** on the mounted volume so browsers stream while reading.

---

## External references

- macFUSE FSKit (macOS 15.4+):  
  https://github.com/macfuse/macfuse/wiki/Getting-Started

- MediaHub API:
  - OAuth2 Client Credentials (short‑lived token from `client_id`/`client_secret`):  
    https://mediahub.xvid.com/manual/api/oauth2/POST.html
  - Jobs list with embedded files (`expand=file`):  
    https://mediahub.xvid.com/manual/api/jobs/GET.html  
    https://mediahub.xvid.com/manual/api/files/GET.html
  - Temporary download link (`autograph_tag`, `redirect=true`):  
    https://mediahub.xvid.com/manual/api/files/downloads/GET.html
  - (Optional) HMAC-signed URLs alternative:  
    https://mediahub.xvid.com/manual/api/oauth2/HMAC.html

The files/downloads/ endpoint (after following redirect) supports **HTTP Range** and returns **Content-Length**. File size is also available from the `jobs?expand=file` response already.

---

## Tech choices (MVP)

- **Language:** Go 1.22+
- **Filesystem:** `cgofuse` (libfuse-compatible; should work with macFUSE FSKit & FUSE3 on Linux)
- **UI:** **Wails v2** (Go-native desktop UI, cross-platform). The UI lists jobs/outputs, prompts recipient id, and provides a draggable tile mapped to the staged file path.
- **HTTP client:** `net/http` with redirect policy and `Range` header support.
- **Token cache:** in‑memory with expiry; auto-refresh on 403/near-expiry. **Never** write the OAuth token to disk.

---

## Repo layout (target)

```
/main.go                   # entrypoint: starts mount + launches UI
/frontend/                 # static HTML frontend (embedded at build time)
/internal/api/             # MediaHub API client (OAuth, jobs, downloads)
/internal/fetcher/         # HTTP Range fetcher (temp-file default, range-lru experimental)
/internal/fs/              # FUSE RO FS (cgofuse) exposing /Staged
/internal/daemon/          # daemon lifecycle management
/ui/                       # Wails app (frontend + bindings)
/pkg/config/               # config loader (toml + env overrides), no token persistence
```

---

## Config

Create **`~/.fuse-stream-mvp/config.toml`**:

```toml
api_base   = "https://api.xvid.com/v1"
client_id  = "YOUR_CLIENT_ID"
client_secret = "YOUR_CLIENT_SECRET"

# Optional runtime overrides:
mountpoint = "/Volumes/FuseStream"   # macOS default; e.g. "/mnt/fusestream" on Linux

# Fetch mode (optional):
fetch_mode = "temp-file"   # default: downloads entire file to temp dir (reliable, recommended)
                           # experimental: "range-lru" (streams chunks on-demand, memory-based cache)
temp_dir = "/tmp"          # temp file directory (only used in temp-file mode)
```

Environment variable overrides (take precedence):
- `FSMVP_API_BASE`
- `FSMVP_CLIENT_ID`
- `FSMVP_CLIENT_SECRET`
- `FSMVP_MOUNTPOINT`
- `FSMVP_FETCH_MODE` (`temp-file` or `range-lru`)
- `FSMVP_TEMP_DIR`

> The short‑lived **OAuth token is cached in memory only**.

### Fetch Modes

**`temp-file` (default, recommended)**:
- Downloads the entire file to a temporary file on first open
- Subsequent reads are served from disk (fast, reliable)
- Temp file persists until both UI staging and FUSE handles are closed (see BackingStore Lifecycle)
- Best for: MVP use case, repeated uploads, stable performance

**`range-lru` (experimental)**:
- Streams 4MB chunks on-demand via HTTP Range requests
- LRU cache keeps 8 chunks (~32MB) in memory
- No disk usage, but may be slower for non-sequential access
- Best for: memory-constrained environments, testing

For most users, the default `temp-file` mode is recommended.

---

## Development (macOS)

### Prerequisites

- **macOS 15.4+** (Sequoia or later)
- **macFUSE with FSKit backend**: Install from [macfuse.io](https://macfuse.io/)
  - No Recovery Mode changes required on macOS 15.4+ (FSKit uses user-space extensions)
  - After installation, macFUSE will be available at `/Library/Filesystems/macfuse.fs`
- **Go 1.22+**
- **Wails CLI**: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

### Build

**Important:** Wails applications **must** be built using the `wails` CLI (not `go build`) to include the required build tags.

```bash
# Install Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Production build (GUI app)
wails build -skipbindings

# Binary will be in build/bin/fuse-stream-mvp.app

# Headless build (for dev/testing, no GUI)
go build -tags fuse -o fuse-stream-mvp-headless .
```

### macOS-specific builds

For FSKit-enabled builds (recommended on M3):

```bash
# Build with FUSE support tag
wails build -skipbindings -tags fuse

# Or for headless:
go build -tags fuse -o fuse-stream-mvp-headless .
```

### Run (local)

**Option 1: Development mode (recommended)**

```bash
# Ensure config.toml exists (see above)
wails dev
```

This launches the app with hot-reload for frontend changes and proper build tags.

**Option 2: Production build**

```bash
# Build first
wails build -skipbindings

# Then run
./build/bin/fuse-stream-mvp              # Linux
# or
open ./build/bin/fuse-stream-mvp.app     # macOS
```

**Option 3: Headless mode (no GUI)**

```bash
# Build with FUSE support
go build -tags fuse -o fuse-stream-mvp-headless .

# Run without GUI (daemon only)
./fuse-stream-mvp-headless --headless
```

This starts the daemon (FUSE mount + HTTP server) without launching a window. Useful for CI or manual testing.

The GUI app mounts (e.g., `/Volumes/FuseStream`) and opens a window:
- List jobs → pick output (if multiple) → enter recipient id → **Stage for upload**
- A file appears in `Staged/` and a big draggable tile is shown.
- Drag the tile into the target site's upload zone; upload continues in background.

### Testing

**Unit tests** (with mock server, no credentials required):
```bash
go test ./...
```

**Live contract tests** (requires real MediaHub credentials):
```bash
export MEDIAHUB_CLIENT_ID="your_client_id"
export MEDIAHUB_CLIENT_SECRET="your_client_secret"
go test -v ./internal/api -run TestLive
```

The live tests validate:
- OAuth token acquisition
- Jobs list with `status=SUCCESS` filter
- Download URL generation with `autograph_tag`

In CI, live tests run automatically on push and pull requests, using repository secrets.


---

## Milestones & acceptance

### M1 — Scaffold & CI
- Go module + layout above.
- Config loader (toml + env overrides).
- API client:
  - OAuth2 client-credentials (`POST /v1/oauth2/token`).
  - List jobs `GET /v1/jobs?expand=file` and normalize: job → outputs (file id, size, name, mime).
  - Build temp link `GET /v1/files/downloads` with `autograph_tag` + `redirect=true`; function returns the **final URL** after following redirect (no fetch yet).
- Wails app stub with three screens (List → Output → Recipient).
- CI builds on macOS & Linux (no FUSE) and uploads artifacts.
- **Acceptance:** `go build ./...` passes in CI; one unit test covers redirect-follow logic.

### M2 — FUSE mount + staging
- Implement read-only FUSE FS (cgofuse) exposing `Staged/` and mapping staged items to fixed-size files.
- HTTP Range fetcher with two modes:
  - **temp-file** (default): downloads entire file to temp dir on first open (reliable, recommended)
  - **range-lru** (experimental): streams 4MB chunks on-demand with LRU cache
- Wire UI: “Stage for upload” adds/removes items; show progress (bytes served vs. total).
- **Acceptance:** dragging the staged file into a browser upload zone starts an upload; overlapping transfer observed on a large test file.

### M3 — macOS-only MVP (Current)
**M3 is macOS-only** with production-ready features:
- ✅ **macOS FSKit** with self-hosted CI build (`[self-hosted, macOS, ARM64, macFUSE]`)
- ✅ **Robust Cocoa drag-and-drop**: Idempotent with re-entrancy guards, validation, and proper NSPasteboardTypeFileURL usage
- ✅ **Mount recovery**: Automatic detection and cleanup of stale mounts using `diskutil unmount force`
- ✅ **Sleep prevention**: IOPMAssertionCreateWithName integration (active during file handles open)
- ✅ **Dual-ref eviction**: tileRef + openRef system prevents premature cache eviction
- ✅ **Multi-open support**: BackingStore ref counting handles concurrent/repeated file opens
- ❌ **Linux GTK drag removed**: Linux builds show error message (M3 is macOS-only)

**Acceptance criteria:**
- [ ] CI `macos-fskit-build` job passes on self-hosted runner
- [ ] App mounts successfully on macOS 15.4+ with macFUSE
- [ ] Cocoa drag works reliably (no crashes, no stale pointers)
- [ ] Mount recovery handles stale mounts from force-quit/crash
- [ ] Sleep prevention activates during uploads
- [ ] Staging a new file doesn't break in-progress upload
- [ ] Multiple drag attempts on same tile work correctly

---

## Secrets & testing

- **Never commit credentials.** Use **GitHub Actions secrets** (`MEDIAHUB_CLIENT_ID`, `MEDIAHUB_CLIENT_SECRET`) for CI unit tests that don’t hit live endpoints.
- For local dev, put the client credentials only in `config.toml` or env vars. The short‑lived token stays in memory.

---

## BackingStore Lifecycle

The BackingStore (cache) lifecycle uses a **two-reference counting system** to handle concurrent uploads and staging:

### Two-Ref System

Each staged file tracks two independent reference counts:

1. **tileRef** (UI reference):
   - `1` when the tile is visible/staged in the UI
   - `0` when the tile is replaced or hidden
   - Set by `StageFile()` and `EvictStagedFile()`

2. **openRef** (FUSE handle count):
   - Incremented on each FUSE `Open()` (browser opens the file)
   - Decremented on each FUSE `Release()` (browser closes the file)
   - Tracks active file handles from the OS/browser

### Eviction Rule

**BackingStore is closed and temp file deleted ONLY when BOTH refs are 0.**

This prevents the critical bug where staging a new file would delete the temp file of an in-progress upload.

### Behavior

- **Staging a new file**:
  - Sets `tileRef=0` for previous staged file
  - Does **NOT** close if `openRef > 0` (upload in progress)
  - Creates new file with `tileRef=1, openRef=0`

- **FUSE Open/Release**:
  - `Open()`: `openRef++` (and BackingStore.IncRef())
  - `Release()`: `openRef--` (and BackingStore.DecRef())
  - On `Release()` with `openRef==0 && tileRef==0`: evict

- **Explicit eviction** (user action or app shutdown):
  - Sets `tileRef=0`
  - Evicts only if `openRef==0`
  - Waits for active uploads to complete

### Example Timeline

1. **Stage file A**: `tileRef=1, openRef=0`
2. **Browser opens A**: `tileRef=1, openRef=1` (upload starts)
3. **User stages file B**: A gets `tileRef=0, openRef=1` (still uploading → NOT evicted)
4. **Browser closes A**: A gets `tileRef=0, openRef=0` → **evicted now**
5. **File B remains**: `tileRef=1, openRef=0` (ready for next upload)

### Benefits

- **Repeated uploads**: Dragging the same staged tile multiple times reuses the existing cache (no re-download in temp-file mode).
- **Multi-handle support**: Browser can open/close the file multiple times during upload without cache loss.
- **Single-tile MVP**: For this milestone, staging a new file automatically evicts the old one (max 1 temp file on disk).

### Future Extensions

- Multiple concurrent staged items (needs LRU eviction policy for temp files)
- User-triggered eviction from UI ("Clear Cache" button)
- Time-based eviction (evict after N minutes idle)

### Testing

See `internal/fetcher/store_lifecycle_test.go` for comprehensive tests covering:
- Store survival across refCount going to 0
- Reuse of same store on repeated open/read cycles
- Explicit eviction behavior
- Concurrent multi-handle access
- Temp file lifecycle (TempFile mode)
- Cache behavior (RangeLRU mode)

---

## Notes

- Browsers may perform non-linear reads; ensure Range and random access are correct.
- Report a stable file size (from jobs → files) to the kernel.
- Drag export must be a real **file path** on the mounted volume.

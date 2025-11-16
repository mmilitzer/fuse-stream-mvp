# fuse-stream-mvp (Go + Wails + FUSE)

Drag-to-upload for large videos without pre-downloading: a read-only virtual filesystem that streams from **Xvid MediaHub** over HTTP Range while the browser uploads.

This MVP targets:
- **macOS 15.4+** with **macFUSE FSKit backend** (no kext).
- **Linux** with FUSE3.
- (Windows is out-of-scope for Milestones M1–M3.)

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
/internal/fetcher/         # HTTP Range fetcher with bounded readahead + LRU seek cache
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
```

Environment variable overrides (take precedence):
- `FSMVP_API_BASE`
- `FSMVP_CLIENT_ID`
- `FSMVP_CLIENT_SECRET`
- `FSMVP_MOUNTPOINT`

> The short‑lived **OAuth token is cached in memory only**.

---

## Development

- **macOS**: install **macFUSE** (FSKit backend). No Recovery Mode changes required on 15.4+.
- **Linux**: ensure **fuse3** and user has FUSE permission.

### Build

**Important:** Wails applications **must** be built using the `wails` CLI (not `go build`) to include the required build tags.

```bash
# Install Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Production build (GUI app)
wails build -skipbindings

# Binaries will be in build/bin/
# - Linux: build/bin/fuse-stream-mvp
# - macOS: build/bin/fuse-stream-mvp.app

# Headless build (for dev/testing, no GUI)
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
- HTTP Range fetcher with bounded readahead, LRU seek cache, and retries; supports non-linear reads.
- Wire UI: “Stage for upload” adds/removes items; show progress (bytes served vs. total).
- **Acceptance:** dragging the staged file into a browser upload zone starts an upload; overlapping transfer observed on a large test file.

### M3 — Polish
- Sleep prevention while file handles are open.
- Error surfaces (expired URL → refresh once; 5xx → retry with backoff).
- Cache eviction after reader closes; concurrent staging.
- Optional: HMAC download URL flow as alternative to Bearer.

---

## Secrets & testing

- **Never commit credentials.** Use **GitHub Actions secrets** (`MEDIAHUB_CLIENT_ID`, `MEDIAHUB_CLIENT_SECRET`) for CI unit tests that don’t hit live endpoints.
- For local dev, put the client credentials only in `config.toml` or env vars. The short‑lived token stays in memory.

---

## BackingStore Lifecycle

The BackingStore (cache) lifecycle is now **owned by the staged item**, not by FUSE refCount alone:

### Current Behavior (Post-M2)

- **Staging**: When `StageFile()` is called, a new BackingStore is created on first FUSE `Open()`.
- **FUSE refCount**: Tracks active kernel file handles (multiple opens from browser, media player, etc.).
  - `IncRef()` on each FUSE `Open()`
  - `DecRef()` on each FUSE `Release()`
  - RefCount hitting 0 does **NOT** trigger `Close()` anymore
- **Persistence**: The BackingStore and its cache (temp file or LRU) remain alive as long as the staged item is visible in the UI.
- **Explicit Eviction**: BackingStore is closed only at these points:
  1. When staging a **new file** (evicts the previous one)
  2. When the user calls `EvictStagedFile(id)` (e.g., leaving detail screen)
  3. On **app shutdown** (`EvictAllStagedFiles()`)

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

# fuse-stream-mvp (Go + Wails + FUSE)

Drag-to-upload for large videos without pre-downloading: a read-only virtual filesystem that streams from **Xvid MediaHub** over HTTP Range while the browser uploads.

## Current Status: M3 (macOS-only MVP)

**M3 is macOS-only** with production-ready features:
- **macOS 15.4+** with **macFUSE FSKit backend** (no kext)
- Robust Cocoa drag-and-drop (idempotent, validated, native NSEvent-based)
- Automatic mount recovery (stale mount detection and cleanup)
- **App Nap prevention** - prevents process throttling when app loses focus (critical for Finder-launch)
- **Sleep prevention** during active transfers (IOPMAssertion - prevents system sleep)
- **Persistent file logging** to ~/Library/Logs/FuseStream/fusestream.log (diagnosable without console)
- **Lifecycle monitoring** - tracks foreground/background transitions and tests FS responsiveness
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

## macOS App Nap Prevention & Process Throttling

**Critical for Finder-launched apps:** When the app is launched from Finder (without a console) and loses focus, macOS may throttle the process via **App Nap**, causing FUSE filesystem operations to stall. This manifests as:
- Uploads freezing when switching to the browser
- `ls /Volumes/FuseStream` hanging until the app window is closed
- The app becoming unresponsive when in the background

### Prevention Strategy (Implemented)

The app uses a **dual-layer approach** to prevent process throttling:

1. **Runtime App Nap Prevention (Primary)**
   - Uses `NSProcessInfo beginActivityWithOptions:` with flags:
     - `NSActivityUserInitiated` - User-initiated activity (prevents App Nap)
     - `NSActivityLatencyCritical` - Highest priority, latency-critical
     - `NSActivityIdleSystemSleepDisabled` - Also prevents idle system sleep
   - Token is acquired when filesystem mounts, released on unmount
   - Located in `internal/appnap/` package

2. **Info.plist Backstop (Secondary)**
   - `NSAppSleepDisabled` key set to `true` in `build/darwin/Info.plist`
   - Provides additional insurance against process throttling
   - Applied at app bundle level by Wails build

3. **System Sleep Prevention (During Transfers)**
   - Uses `IOPMAssertion` (IOKit Power Management) during active file transfers
   - Prevents full system sleep while file handles are open
   - Located in `internal/sleep/` package
   - Note: IOPMAssertion prevents **system sleep** but NOT **App Nap** throttling

### Thread Safety

All FUSE operations are **background-thread safe**:
- The FUSE mount runs in a dedicated goroutine (not main thread)
- All FUSE callbacks (Getattr, Readdir, Open, Read, etc.) execute on the mount goroutine
- No synchronous main thread calls from FUSE code paths
- Logging and error handling are async and non-blocking

### Lifecycle Monitoring

The app observes NSApplication lifecycle events:
- `NSApplicationDidBecomeActiveNotification` - app enters foreground
- `NSApplicationDidResignActiveNotification` - app enters background
- After losing focus, filesystem responsiveness is tested to detect latent deadlocks
- All lifecycle events are logged to the persistent log file

### Diagnostic Logging

**Log location:** `~/Library/Logs/FuseStream/fusestream.log`

Key log entries to look for:
- `[appnap] App Nap prevention enabled` - Runtime prevention is active
- `[sleep] Sleep prevention enabled` - System sleep prevention active during transfers
- `[lifecycle] App resigned active (background)` - App lost focus
- `[lifecycle] FS responsive after resign active` - Filesystem still working in background
- `[lifecycle] WARNING: FS unresponsive` - Filesystem stalled (indicates throttling issue)

Logs are automatically rotated (max 10MB per file, 5 files kept).

---

## Development (macOS)

### Prerequisites

**Critical Requirements:**
- **macOS ≥15.4** (Sequoia or later) - Required for FSKit support
- **macFUSE ≥5** with FSKit backend - Install from [macfuse.io](https://macfuse.io/)
  - FSKit backend eliminates kernel extension (kext) requirement
  - No Recovery Mode changes needed
  - Verified with: `/Library/Filesystems/macfuse.fs` should exist after installation
  - App uses `-o backend=fskit` mount option

**Build Tools:**
- **Go 1.22+**
- **Wails CLI**: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

**Why FSKit?**
- macOS 15.4+ uses FSKit (File System Kit) - a user-space file system framework
- No kernel extensions = no System Integrity Protection (SIP) warnings
- More stable and secure than legacy kext approach
- See: [macFUSE FSKit backend documentation](https://github.com/macfuse/macfuse/wiki/Getting-Started)

**FSKit Mountpoint Requirements:**
- **FSKit only supports mountpoints under `/Volumes/`** - Apple's hard limitation
- `/Volumes/` is root-owned; applications **cannot** create directories there directly
- The macFUSE mount helper (`mount_macfuse`) runs with setuid root privileges and creates the mountpoint automatically during mounting
- **Do not manually create** `/Volumes/FuseStream/` - the mount process handles this
- For testing with non-`/Volumes` paths (e.g., `~/test-mount`), the application will create the directory automatically

### Build

**Important:** GUI and CLI builds use separate entry points with build tags:
- **GUI app**: `main.go` at project root (built with Wails, has `//go:build gui` tag)
- **CLI app**: `cmd/cli/main.go` (built with `go build`, has `//go:build !gui` tag)

```bash
# Install Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Build GUI app (.app bundle)
wails build -tags "fuse,gui,production" -skipbindings

# Binary will be in build/bin/fuse-stream-mvp.app

# Build CLI tool (headless, for testing/automation)
go build -tags fuse -o fuse-stream-cli ./cmd/cli
```

### Build Tags Explained

- **GUI build**: Requires `gui` tag to compile root `main.go`
  - Command: `wails build -tags "fuse,gui,production"`
  - Tags: `fuse` (FUSE support), `gui` (enables GUI main), `production` (Wails optimization)
- **CLI build**: Uses `!gui` tag to exclude root main.go
  - Command: `go build -tags fuse -o fuse-stream-cli ./cmd/cli`
  - Tags: `fuse` (FUSE support), no `gui` tag (enables CLI main)
- Build tag guards ensure only one `main()` per build:
  - Root `main.go`: `//go:build gui`
  - `cmd/cli/main.go`: `//go:build !gui`

### Run (local)

**Option 1: Development mode (recommended)**

```bash
# Ensure config.toml exists (see above)
wails dev
```

This launches the app with hot-reload for frontend changes and proper build tags.

**Option 2: Production GUI build**

```bash
# Build GUI app first
wails build -tags "fuse,gui,production" -skipbindings

# Then run
open ./build/bin/fuse-stream-mvp.app     # macOS
```

**Option 3: CLI mode (no GUI)**

```bash
# Build CLI tool
go build -tags fuse -o fuse-stream-cli ./cmd/cli

# Run CLI (mount and keep alive)
./fuse-stream-cli --mount

# Or unmount
./fuse-stream-cli --unmount
```

This starts the daemon (FUSE mount + HTTP server) without launching a window. Useful for CI or manual testing.

The GUI app mounts at `/Volumes/FuseStream` and opens a window:
- List jobs → pick output (if multiple) → enter recipient id → **Stage for upload**
- A file appears in `Staged/` and a big draggable tile is shown.
- Drag the tile into the target site's upload zone; upload continues in background.

### Troubleshooting

**Mount fails with "unknown option" or "backend not found":**
- Verify macFUSE ≥5 is installed: `ls /Library/Filesystems/macfuse.fs`
- Check macOS version: `sw_vers` (should show ≥15.4)
- Reinstall macFUSE from [macfuse.io](https://macfuse.io/)

**"Operation not permitted" or permission errors:**
- Ensure System Settings → Privacy & Security allows macFUSE
- Check that mountpoint directory is writable

**App crashes during drag:**
- Check Console.app for crash logs
- Verify file is visible in Finder at `/Volumes/FuseStream/Staged/`
- Try restarting the app (mount recovery will handle stale mounts)

**Sleep prevention not working:**
- Check logs for `[sleep] Sleep prevention enabled` and `[appnap] App Nap prevention enabled`
- Verify IOKit and Foundation frameworks are available
- macOS may override sleep prevention for low battery or scheduled sleep

**App becomes unresponsive when launched from Finder:**
- Check `~/Library/Logs/FuseStream/fusestream.log` for diagnostic logs
- Look for `[lifecycle]` entries showing foreground/background transitions
- Verify App Nap prevention is enabled: `[appnap] App Nap prevention enabled`
- If filesystem becomes unresponsive after losing focus, check logs for `[lifecycle] WARNING: FS unresponsive`

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
- ✅ **macOS FSKit backend**: Uses `-o backend=fskit` for user-space FUSE (no kext)
- ✅ **Self-hosted CI build**: Single macOS job on `[self-hosted, macOS, ARM64, macFUSE]` runner
- ✅ **Robust Cocoa drag-and-drop**: Idempotent with re-entrancy guards, validation, and proper NSPasteboardTypeFileURL usage
- ✅ **Mount recovery**: Automatic detection and cleanup of stale mounts using `diskutil unmount force`
- ✅ **Sleep prevention**: IOPMAssertionCreateWithName integration (active during file handles open)
- ✅ **Dual-ref eviction**: tileRef + openRef system prevents premature cache eviction
- ✅ **Multi-open support**: BackingStore ref counting handles concurrent/repeated file opens
- ✅ **Error guidance**: Clear error messages if mount fails (FSKit/macFUSE version requirements)
- ❌ **Linux GTK drag removed**: Linux builds show error message (M3 is macOS-only)

**Acceptance criteria:**
- [ ] CI `macos-fskit-build` job passes on self-hosted runner (single macOS job)
- [ ] App mounts successfully using FSKit backend (verify with `-o backend=fskit`)
- [ ] No kernel extension prompts or Recovery Mode requirements
- [ ] Cocoa drag works reliably (Safari/Chrome, no crashes, no stale pointers)
- [ ] Mount recovery handles stale mounts from force-quit/crash
- [ ] Sleep prevention activates during uploads (check logs)
- [ ] Staging a new file doesn't break in-progress upload
- [ ] Multiple drag attempts on same tile work correctly
- [ ] Mount failure shows actionable error message with macFUSE/FSKit guidance

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

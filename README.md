# FuseStream - Fast Video Uploads for Xvid MediaHub

Upload large videos directly to websites without downloading them first. FuseStream creates a virtual file on your Mac that streams content from your Xvid MediaHub account, allowing browsers to upload videos while simultaneously streaming from the cloud. This means **uploads start immediately** and finish in roughly the same time as a direct download would take - saving both time and disk space.

## What Does It Do?

FuseStream solves a common problem: you have large video files stored in Xvid MediaHub and need to upload them to another website (client portal, social media, cloud storage, etc.). Normally, you would:
1. Download the entire video from MediaHub to your Mac (takes time, uses disk space)
2. Wait for the download to complete
3. Upload the file from your Mac to the destination site

**With FuseStream:**
1. Select your video in the app
2. Drag and drop into the website's upload zone
3. The upload starts immediately while the app streams from MediaHub in the background
4. Total time ≈ max(download time, upload time) instead of download + upload time
5. No large files filling up your hard drive

## Key Benefits

- **Faster workflows**: Upload starts immediately, no waiting for downloads
- **Save disk space**: No need to store multi-gigabyte video files locally
- **Works with any website**: Standard drag-and-drop upload to any site that accepts file uploads
- **Personalized copies**: Automatically embeds recipient information for tracking and security
- **Native Mac experience**: Clean interface, no command-line required

## System Requirements

- **macOS 15.4 or later** (Sequoia)
- **macFUSE 5.0+** with FSKit backend
- **Xvid MediaHub account** with API credentials

---

## Getting Started

Follow these step-by-step instructions to get FuseStream running on your Mac.

### Step 1: Install macFUSE

macFUSE is required for FuseStream to create the virtual filesystem on your Mac.

1. **Download macFUSE:**
   - Go to the [official macFUSE releases page](https://github.com/macfuse/macfuse/releases)
   - Download the latest version (5.1.2 or newer):
     ```
     https://github.com/macfuse/macfuse/releases/download/macfuse-5.1.2/macfuse-5.1.2.dmg
     ```

2. **Install macFUSE:**
   - Double-click the downloaded `.dmg` file
   - Double-click "Install macFUSE" in the opened window
   - Follow the installation wizard prompts
   - Grant permissions when requested by macOS
   - If you see a warning that System Extensions are blocked, click **"Ignore"** and continue

3. **Enable the File System Extension:**
   - Open **System Settings** (Apple menu → System Settings)
   - Go to **Login Items & Extensions**
   - Scroll down to the **Extensions** section at the bottom
   - Click **File System Extensions**
   - Toggle **ON** the switch for "macFUSE (local)"
   - Click **Done**

### Step 2: Download FuseStream

1. **Download the latest release:**
   - Visit the [FuseStream releases page](https://github.com/mmilitzer/fuse-stream-mvp/releases)
   - Download `macos-build.zip` from the latest release (e.g., beta1):
     ```
     https://github.com/mmilitzer/fuse-stream-mvp/releases/download/beta1/macos-build.zip
     ```

2. **Install the application:**
   - Unzip the downloaded file (double-click `macos-build.zip`)
   - Move `fuse-stream-mvp.app` to your **Applications** folder (or any location you prefer)

### Step 3: Configure FuseStream

FuseStream needs your Xvid MediaHub API credentials to access your videos.

#### 3.1: Get Your API Credentials

If you don't have API credentials yet:
1. Log in to your [Xvid MediaHub account](https://mediahub.xvid.com)
2. Follow the [MediaHub API tutorial](https://mediahub.xvid.com/docs/Easy+step-by-step+Walkthrough) (Step 2) to create a new application
3. Copy your `client_id` and `client_secret`

#### 3.2: Create Configuration File

1. **Copy this template** to your clipboard:

```toml
api_base   = "https://api.xvid.com/v1"
client_id  = "YOUR_CLIENT_ID"
client_secret = "YOUR_CLIENT_SECRET"

# Mount location (adjust to your username)
mountpoint = "/Users/YourUsername/FuseStream"

# Optional settings
fetch_mode = "temp-file"
temp_dir = "/tmp"
```

2. **Find your home directory:**
   - Open **Terminal** (Launchpad → Other → Terminal)
   - Type `pwd` and press Enter
   - You'll see something like `/Users/YourUsername` - copy this path

3. **Edit the template:**
   - Replace `YOUR_CLIENT_ID` with your actual client ID from step 3.1
   - Replace `YOUR_CLIENT_SECRET` with your actual client secret
   - Replace `/Users/YourUsername` in the mountpoint line with the path from step 2

4. **Save the configuration file:**
   - In Terminal, create the config directory:
     ```bash
     mkdir -p ~/.fuse-stream-mvp
     ```
   - Open a text editor to create the config file. You can use `nano`:
     ```bash
     nano ~/.fuse-stream-mvp/config.toml
     ```
   - Paste your edited configuration
   - Save and exit:
     - Press `Control + O` (to save)
     - Press `Enter` (to confirm)
     - Press `Control + X` (to exit)

Alternatively, if you saved the template to a file called `config.toml` on your Desktop:
```bash
mkdir -p ~/.fuse-stream-mvp
cp ~/Desktop/config.toml ~/.fuse-stream-mvp/
```

### Step 4: Run FuseStream

1. **Launch the application:**
   - Open **Finder** and navigate to where you installed the app
   - Double-click **fuse-stream-mvp.app**
   - If macOS shows a security warning about an unidentified developer:
     - Open **System Settings** → **Privacy & Security**
     - Scroll down and click **Open Anyway** next to the FuseStream message
     - Click **Open** in the confirmation dialog

2. **Using FuseStream:**
   - The app will start and show your Xvid MediaHub videos
   - **Select a video** from the list
   - If the video has multiple outputs/formats, **choose the one you want**
   - **Enter recipient information**: Type a unique identifier for the recipient (e.g., username, email, or ID)
     - This creates a personalized copy with embedded tracking information
   - Click **"Stage for Upload"**
   - A draggable tile will appear showing the recipient ID
   - **Drag the tile** into any website's upload zone (drag-and-drop area)
   - The upload will start immediately while the app streams from MediaHub in the background
   - You can monitor progress in the app window

3. **Tips:**
   - Keep the app running while uploads are in progress
   - The app prevents your Mac from sleeping during active transfers
   - Check logs if you encounter issues: `~/Library/Logs/FuseStream/fusestream.log`

---

## Troubleshooting

**Mount fails with "unknown option" or "backend not found":**
- Verify macFUSE ≥5 is installed: `ls /Library/Filesystems/macfuse.fs`
- Check macOS version: `sw_vers` (should show ≥15.4)
- Reinstall macFUSE from [macfuse.io](https://macfuse.io/)

**"Operation not permitted" or permission errors:**
- Ensure System Settings → Privacy & Security allows macFUSE
- Check that mountpoint directory is writable
- Try using a different mountpoint location (edit config.toml)

**App crashes during drag:**
- Check Console.app for crash logs
- Verify file is visible in Finder at the mountpoint location (e.g., `/Users/YourUsername/FuseStream/Staged/`)
- Try restarting the app (mount recovery will handle stale mounts)

**Uploads freeze or app becomes unresponsive:**
- Check `~/Library/Logs/FuseStream/fusestream.log` for diagnostic information
- Ensure App Nap prevention is enabled (logged as `[appnap] App Nap prevention enabled`)
- Keep the app window visible or check logs for `[lifecycle]` warnings

**Cannot find config file:**
- Verify the file exists: `ls -la ~/.fuse-stream-mvp/config.toml`
- Check file permissions: `chmod 600 ~/.fuse-stream-mvp/config.toml`
- Verify the file format is valid TOML

---

# Developer Documentation

The following sections contain technical information for developers who want to build, modify, or contribute to FuseStream.

---

## Technical Overview

FuseStream is built with Go, using Wails for the native macOS UI and cgofuse for FUSE filesystem support. The application creates a read-only virtual filesystem that streams content from Xvid MediaHub over HTTP Range requests while browsers perform uploads.

### Data Flow

1. **Auth**: Exchange `client_id`/`client_secret` (from config) for a short-lived OAuth token (cached in memory only)
2. **List titles**: `GET /v1/jobs?expand=file` returns video titles and embedded file objects (IDs, sizes, content types)
3. **User selection**: Pick a job; if multiple outputs exist, user selects the desired output (file ID)
4. **Recipient**: Prompt for a recipient identifier (free text), which becomes `autograph_tag`
5. **Temp URL**: Call `GET /v1/files/downloads` with `file_id`, `autograph_tag`, `redirect=true`. Follow the 30x redirect to the final URL
6. **Mount**: Expose a read-only FUSE filesystem with a `Staged/` directory. Each staged selection appears as a single fixed-size file
7. **Drag**: The UI shows a draggable tile that provides the real file path to the browser's upload zone
8. **Overlap**: As the browser reads the path, the app streams from the temp URL using HTTP Range. Wall time ≈ max(download, upload)
9. **Evict**: After upload completes (file handle closed), evict any local cache and remove the staged entry

> **Technical Note:** The drag operation provides a real file path on the mounted volume (not "promised files"), allowing browsers to stream while reading.


## API & External References

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

## Technology Stack

- **Language:** Go 1.22+
- **Filesystem:** `cgofuse` (libfuse-compatible; should work with macFUSE FSKit & FUSE3 on Linux)
- **UI:** **Wails v2** (Go-native desktop UI, cross-platform). The UI lists jobs/outputs, prompts recipient id, and provides a draggable tile mapped to the staged file path.
- **HTTP client:** `net/http` with redirect policy and `Range` header support.
- **Token cache:** in‑memory with expiry; auto-refresh on 403/near-expiry. **Never** write the OAuth token to disk.

---

## Repository Structure

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

## Configuration Reference

For basic configuration, see the [Getting Started](#step-3-configure-fusestream) section above.

### Advanced Configuration Options

**Environment variable overrides** (take precedence over config.toml):
- `FSMVP_API_BASE` - API base URL
- `FSMVP_CLIENT_ID` - OAuth client ID
- `FSMVP_CLIENT_SECRET` - OAuth client secret
- `FSMVP_MOUNTPOINT` - Filesystem mount location
- `FSMVP_FETCH_MODE` - `temp-file` (default) or `range-lru` (experimental)
- `FSMVP_TEMP_DIR` - Temporary file directory
- `FSMVP_ENABLE_APP_NAP` - Set to `true`, `1`, or `yes` to allow App Nap (not recommended)
- `FSMVP_DEBUG_FSKIT_SINGLE_THREAD` - Set to `true` for single-threaded FSKit mode (debugging only)

### Fetch Modes

**`temp-file` (default, recommended)**:
- Downloads entire file to temporary storage on first open
- Fast, reliable performance for repeated access
- Temp file persists until both UI staging and FUSE handles close
- Best for normal usage

**`range-lru` (experimental)**:
- Streams 4MB chunks on-demand via HTTP Range requests
- LRU cache with 8 chunks (~32MB) in memory
- No disk usage, but slower for non-sequential access
- Best for testing or memory-constrained environments

> **Security Note:** OAuth tokens are cached **in memory only** and never written to disk.


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

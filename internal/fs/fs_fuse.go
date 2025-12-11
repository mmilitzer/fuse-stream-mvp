//go:build fuse

package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/appnap"
	"github.com/mmilitzer/fuse-stream-mvp/internal/fetcher"
	"github.com/mmilitzer/fuse-stream-mvp/internal/logging"
	"github.com/mmilitzer/fuse-stream-mvp/internal/sleep"
	"github.com/mmilitzer/fuse-stream-mvp/pkg/config"
	"github.com/winfsp/cgofuse/fuse"
)

const stagedDirName = "Staged"

type stagedFileFuse struct {
	*StagedFile
	store   fetcher.BackingStore
	storeMu sync.Mutex
	
	// Two-ref tracking for lifecycle:
	// - tileRef: 1 when tile is visible/staged, 0 when replaced or hidden
	// - openRef: count of active FUSE handles (Open/Release)
	// Eviction only happens when both are 0
	tileRef int32
	openRef int32
}

type fuseFS struct {
	mountpoint  string
	mounted     bool
	client      *api.Client
	config      *config.Config
	stagedFiles map[string]*stagedFileFuse
	mu          sync.RWMutex
	host        *fuse.FileSystemHost
	ctx         context.Context
	cancel      context.CancelFunc
	
	// File handle management
	nextFH        uint64
	fhToStore     map[uint64]fetcher.BackingStore
	fhToPath      map[uint64]string
	fhToStagedID  map[uint64]string  // Maps file handle to staged file ID
	fhMu          sync.RWMutex
	
	// Sleep prevention (IOPM assertion - prevents system sleep)
	sleepRelease  func()
	sleepMu       sync.Mutex
	
	// App Nap prevention (NSProcessInfo activity - prevents process throttling)
	appNapRelease func()
	appNapMu      sync.Mutex
}

func newFS(client *api.Client, cfg *config.Config) FS {
	ctx, cancel := context.WithCancel(context.Background())
	return &fuseFS{
		client:       client,
		config:       cfg,
		stagedFiles:  make(map[string]*stagedFileFuse),
		fhToStore:    make(map[uint64]fetcher.BackingStore),
		fhToPath:     make(map[uint64]string),
		fhToStagedID: make(map[uint64]string),
		nextFH:       1,
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (fs *fuseFS) Start(opts MountOptions) error {
	fs.mountpoint = opts.Mountpoint
	
	// Initialize non-blocking FUSE logger FIRST (before any FUSE operations)
	// This ensures all FUSE callbacks can log without blocking
	if err := logging.InitFUSELogger(); err != nil {
		log.Printf("[fs] Warning: failed to initialize FUSE logger: %v", err)
		// Continue anyway - logging is not critical for functionality
	} else {
		log.Printf("[fs] FUSE logger initialized successfully")
	}
	
	// Clean up stale temp files from previous runs
	tempDir := fs.config.TempDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	tempManager := fetcher.GetTempFileManager(tempDir)
	if err := tempManager.CleanupStaleFiles(); err != nil {
		log.Printf("[fs] Warning: failed to cleanup stale temp files: %v", err)
		// Continue anyway - this is not a fatal error
	}
	
	// Attempt mount recovery on macOS
	if runtime.GOOS == "darwin" {
		if err := fs.recoverStaleMountMacOS(); err != nil {
			log.Printf("[fs] Warning: mount recovery failed: %v", err)
			// Continue anyway - the mount attempt below will fail if there's still an issue
		}
	}
	
	fs.host = fuse.NewFileSystemHost(fs)
	
	// Mount options (OS-specific)
	mountOpts := []string{
		"-o", "ro",
		"-o", "fsname=fusestream",
	}

	switch runtime.GOOS {
	case "darwin":
		// macFUSE/macOS-specific options with FSKit backend
		// FSKit backend requires macFUSE ≥5 on macOS ≥15.4 (no kernel extension)
		mountOpts = append(mountOpts,
			"-o", "local",
			"-o", "volname=FuseStream",
			"-o", "backend=fskit",  // Required for FSKit backend
		)
	case "linux":
		// Linux: NO volname (not supported), and NO allow_other by default
		// If you need allow_other, it requires user_allow_other in /etc/fuse.conf
		// and can be gated by a config option in the future
	}

	// Mount the filesystem
	// Note: host.Mount() is a blocking call that runs the FUSE event loop.
	// It only returns when the filesystem is unmounted or if mount fails immediately.
	// So we need to run it in a goroutine and verify the mount succeeded by checking
	// if the filesystem is accessible.
	mountErr := make(chan error, 1)
	go func() {
		log.Printf("[fs] Starting FUSE mount at %s", fs.mountpoint)
		success := fs.host.Mount(fs.mountpoint, mountOpts)
		if !success {
			log.Printf("[fs] Mount call returned false (mount failed)")
			if runtime.GOOS == "darwin" {
				log.Printf("[fs] ERROR: Mount failed. This may indicate:")
				log.Printf("[fs]   - macFUSE is not installed or is too old (requires macFUSE ≥5)")
				log.Printf("[fs]   - macOS version is too old (requires macOS ≥15.4 for FSKit)")
				log.Printf("[fs]   - FSKit backend is not available (try 'backend=fskit' option)")
				log.Printf("[fs]   - Mountpoint is already in use or inaccessible")
				log.Printf("[fs] Install macFUSE with FSKit support from: https://macfuse.io/")
			}
			mountErr <- fmt.Errorf("mount call failed at %s", fs.mountpoint)
		} else {
			log.Printf("[fs] Mount call returned (filesystem unmounted)")
		}
	}()

	// Wait for filesystem to be ready by checking if we can access the mountpoint
	// The Mount() call above is blocking and won't return until unmount, so we
	// verify the mount by checking if the filesystem responds to operations
	log.Printf("[fs] Waiting for filesystem to be ready...")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-mountErr:
			// Mount failed immediately
			return err
		default:
			// Check if mount is ready by trying to read the directory
			// This triggers a FUSE operation (Readdir) which confirms the filesystem is responding
			time.Sleep(100 * time.Millisecond)
			if _, err := os.ReadDir(fs.mountpoint); err == nil {
				log.Printf("[fs] Filesystem is ready and responding to operations")
				fs.mounted = true
				
				// Enable App Nap prevention if configured
				if fs.config.EnableAppNap {
					fs.enableAppNapPrevention()
					log.Printf("[fs] App Nap prevention is ENABLED (config: enable_app_nap=true)")
				} else {
					log.Printf("[fs] App Nap prevention is DISABLED (config: enable_app_nap=false)")
				}
				
				return nil
			}
		}
	}
	
	// Timeout - mount didn't become ready in time
	return fmt.Errorf("mount timed out at %s - filesystem not responding", fs.mountpoint)
}

// recoverStaleMountMacOS attempts to unmount any stale mount at the mountpoint
// before we try to mount. This handles the case where the app crashed or was
// force-quit without properly unmounting.
//
// FSKit Mounting Requirements:
// - Mountpoints under /Volumes MUST NOT be created manually by the application
// - /Volumes is root-owned and only the macFUSE mount helper (mount_macfuse) can create directories there
// - The mount helper runs with setuid root privileges and creates the mountpoint automatically
// - For non-/Volumes paths (e.g., testing), we create the directory manually
func (fs *fuseFS) recoverStaleMountMacOS() error {
	// Check if mountpoint is already mounted by checking if it's accessible
	_, err := os.Stat(fs.mountpoint)
	if err != nil {
		// Mountpoint doesn't exist or isn't accessible
		if os.IsNotExist(err) {
			// For paths under /Volumes, DO NOT create the directory manually
			// The macFUSE mount helper will create it automatically
			if strings.HasPrefix(fs.mountpoint, "/Volumes/") {
				log.Printf("[fs] Mount recovery: mountpoint %s doesn't exist (expected for /Volumes paths - mount helper will create it)", fs.mountpoint)
				return nil
			}
			
			// For non-/Volumes paths (e.g., testing), create the directory
			log.Printf("[fs] Mount recovery: creating non-/Volumes mountpoint %s", fs.mountpoint)
			return os.MkdirAll(fs.mountpoint, 0755)
		}
		return nil
	}
	
	// Try to detect if it's a stale mount by attempting to list directory
	// A stale FUSE mount will typically hang or error
	doneCh := make(chan error, 1)
	go func() {
		_, err := os.ReadDir(fs.mountpoint)
		doneCh <- err
	}()
	
	select {
	case err := <-doneCh:
		// If we can read the directory, it might be a valid mount or just a regular directory
		// In either case, try to unmount it
		if err == nil {
			log.Printf("[fs] Mount recovery: detected possible stale mount at %s, attempting force unmount", fs.mountpoint)
		} else {
			log.Printf("[fs] Mount recovery: error accessing mountpoint %s: %v, attempting force unmount", fs.mountpoint, err)
		}
	case <-time.After(2 * time.Second):
		// Timeout - likely a stale mount that's hanging
		log.Printf("[fs] Mount recovery: timeout accessing mountpoint %s, attempting force unmount", fs.mountpoint)
	}
	
	// Try diskutil unmount force (preferred on macOS)
	log.Printf("[fs] Attempting: diskutil unmount force %s", fs.mountpoint)
	cmd := fmt.Sprintf("diskutil unmount force '%s' 2>&1 || umount -f '%s' 2>&1 || true", fs.mountpoint, fs.mountpoint)
	output, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		log.Printf("[fs] Mount recovery command failed (may be expected): %v, output: %s", err, string(output))
	} else {
		log.Printf("[fs] Mount recovery successful or no mount present: %s", string(output))
	}
	
	// Give the system a moment to finish unmounting
	time.Sleep(200 * time.Millisecond)
	
	return nil
}

func (fs *fuseFS) Stop() error {
	// Release App Nap prevention if active (do this early)
	fs.appNapMu.Lock()
	if fs.appNapRelease != nil {
		fs.appNapRelease()
		fs.appNapRelease = nil
		log.Println("[appnap] App Nap prevention released")
	}
	fs.appNapMu.Unlock()
	
	// Release sleep prevention if active (do this early)
	fs.sleepMu.Lock()
	if fs.sleepRelease != nil {
		fs.sleepRelease()
		fs.sleepRelease = nil
		log.Println("[sleep] Sleep prevention released")
	}
	fs.sleepMu.Unlock()
	
	// Cancel context to signal any ongoing operations to stop
	fs.cancel()
	
	// Close all open file handles to allow unmount
	// This is critical - open file handles will prevent unmount
	fs.fhMu.Lock()
	openHandles := len(fs.fhToStore)
	if openHandles > 0 {
		log.Printf("[fs] Closing %d open file handles before unmount", openHandles)
		// Clear the maps - this doesn't call Release but ensures no new operations
		fs.fhToStore = make(map[uint64]fetcher.BackingStore)
		fs.fhToPath = make(map[uint64]string)
		fs.fhToStagedID = make(map[uint64]string)
	}
	fs.fhMu.Unlock()
	
	// Evict all staged files to clean up BackingStores
	// This closes the backing stores which will close temp files
	if err := fs.EvictAllStagedFiles(); err != nil {
		log.Printf("[fs] Error evicting staged files: %v", err)
	}
	
	// Give FUSE operations time to complete
	// The verification ReadDir() during mount may have left operations pending
	log.Printf("[fs] Waiting for FUSE operations to complete...")
	time.Sleep(200 * time.Millisecond)
	
	// Unmount the filesystem
	if fs.host != nil && fs.mounted {
		log.Println("[fs] Unmounting filesystem...")
		
		// Try normal unmount with timeout
		unmountDone := make(chan bool, 1)
		go func() {
			unmountDone <- fs.host.Unmount()
		}()
		
		select {
		case success := <-unmountDone:
			if success {
				log.Println("[fs] Filesystem unmounted successfully")
				fs.mounted = false
			} else {
				log.Println("[fs] Normal unmount failed, attempting force unmount...")
				// Try force unmount on macOS
				if runtime.GOOS == "darwin" {
					if err := fs.ForceUnmount(); err != nil {
						log.Printf("[fs] Force unmount also failed: %v", err)
						// Continue cleanup anyway - OS will clean up eventually
					}
				}
			}
		case <-time.After(3 * time.Second):
			log.Println("[fs] Unmount timed out after 3 seconds, attempting force unmount...")
			// Try force unmount on macOS
			if runtime.GOOS == "darwin" {
				if err := fs.ForceUnmount(); err != nil {
					log.Printf("[fs] Force unmount also failed: %v", err)
				}
			}
		}
	}
	
	// Clean up all temp files from TempFileManager
	// Do this AFTER unmount to ensure all file handles are closed
	tempDir := fs.config.TempDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	tempManager := fetcher.GetTempFileManager(tempDir)
	if err := tempManager.CleanupAllFiles(); err != nil {
		log.Printf("[fs] Warning: error cleaning up temp files: %v", err)
		// Continue with shutdown even if cleanup fails
	}
	
	// Close FUSE logger AFTER unmount completes
	// This ensures we capture all FUSE operation logs
	log.Println("[fs] Closing FUSE logger...")
	if err := logging.CloseFUSELogger(); err != nil {
		log.Printf("[fs] Warning: error closing FUSE logger: %v", err)
	}
	
	return nil
}

// StopAsync attempts to unmount the filesystem asynchronously and returns immediately.
// The caller should wait for the returned channel to signal completion.
func (fs *fuseFS) StopAsync() <-chan error {
	errChan := make(chan error, 1)
	
	go func() {
		errChan <- fs.Stop()
	}()
	
	return errChan
}

// ForceUnmount attempts to forcibly unmount the filesystem using OS-specific commands.
// This should only be called if normal unmount fails or times out.
func (fs *fuseFS) ForceUnmount() error {
	if runtime.GOOS == "darwin" {
		log.Printf("[fs] Attempting force unmount at %s", fs.mountpoint)
		cmd := fmt.Sprintf("diskutil unmount force '%s' 2>&1 || umount -f '%s' 2>&1", fs.mountpoint, fs.mountpoint)
		output, err := exec.Command("sh", "-c", cmd).CombinedOutput()
		if err != nil {
			log.Printf("[fs] Force unmount failed: %v, output: %s", err, string(output))
			return fmt.Errorf("force unmount failed: %w", err)
		}
		log.Printf("[fs] Force unmount successful: %s", string(output))
		fs.mounted = false
		return nil
	}
	
	return fmt.Errorf("force unmount not implemented for %s", runtime.GOOS)
}

func (fs *fuseFS) Mountpoint() string {
	return fs.mountpoint
}

func (fs *fuseFS) Mounted() bool {
	return fs.mounted
}

// HasActiveUploads returns true if any staged files have active open file handles (openRef > 0).
// This indicates uploads are in progress and the app should not be closed without confirmation.
func (fs *fuseFS) HasActiveUploads() bool {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	
	for _, sf := range fs.stagedFiles {
		if atomic.LoadInt32(&sf.openRef) > 0 {
			return true
		}
	}
	return false
}

func (fs *fuseFS) StageFile(fileID, fileName, recipientTag string, size int64, contentType string) (*StagedFile, error) {
	id := fmt.Sprintf("%s_%s", fileID, recipientTag)
	
	// Check if this file is already staged (quick check without lock)
	fs.mu.RLock()
	existingSff, exists := fs.stagedFiles[id]
	fs.mu.RUnlock()
	
	if exists {
		// File already staged - check if BackingStore is valid
		existingSff.storeMu.Lock()
		storeValid := existingSff.store != nil
		if storeValid {
			// Check if temp file was evicted (TempFileManager sets evicted flag via callback)
			if tempStore, ok := existingSff.store.(*fetcher.TempFileStore); ok && tempStore.IsEvicted() {
				log.Printf("[fs] StageFile: File %s BackingStore was evicted, will re-create", id)
				// Close the evicted store
				existingSff.store.Close()
				existingSff.store = nil
				storeValid = false
			}
		}
		existingSff.storeMu.Unlock()
		
		atomic.StoreInt32(&existingSff.tileRef, 1)
		existingSff.ModTime = time.Now()
		
		if storeValid {
			log.Printf("[fs] StageFile: File %s already staged with valid BackingStore, resetting tileRef=1", id)
			return existingSff.StagedFile, nil
		}
		// Fall through to create new store
		log.Printf("[fs] StageFile: File %s already staged but BackingStore needs re-creation", id)
	}
	
	// For new files or files needing store re-creation, check disk space FIRST
	// This ensures we don't block FUSE operations later
	// Only check for temp-file mode, not range-lru mode
	if fs.config.FetchMode == "temp-file" {
		tempDir := fs.config.TempDir
		if tempDir == "" {
			tempDir = os.TempDir()
		}
		
		tempManager := fetcher.GetTempFileManager(tempDir)
		
		// Try to ensure space is available (will evict old files if needed)
		// This can do disk I/O but it's OK here since we're NOT in a FUSE callback
		if err := tempManager.EnsureSpaceAvailable(size); err != nil {
			// If we can't free enough space, return a user-friendly error
			log.Printf("[fs] StageFile: Insufficient disk space for %s (%d bytes): %v", fileName, size, err)
			return nil, fmt.Errorf("insufficient disk space: %w", err)
		}
		log.Printf("[fs] StageFile: Disk space check passed for %s (%d bytes)", fileName, size)
	}
	
	// Build temp URL (network I/O - but OK here since we're NOT in FUSE callback)
	tempURL, err := fs.client.BuildTempURL(fileID, recipientTag)
	if err != nil {
		log.Printf("[fs] StageFile: Failed to build temp URL for %s: %v", fileName, err)
		return nil, fmt.Errorf("failed to build temp URL: %w", err)
	}
	log.Printf("[fs] StageFile: Got temp URL for %s", fileName)
	
	// Create store options from config
	storeOpts := fetcher.StoreOptions{
		Mode:                  fetcher.FetchMode(fs.config.FetchMode),
		TempDir:               fs.config.TempDir,
		ChunkSize:             fs.config.ChunkSize,
		MaxConcurrentRequests: fs.config.MaxConcurrentRequests,
		CacheSize:             fs.config.CacheSize,
	}
	if storeOpts.ChunkSize == 0 {
		storeOpts.ChunkSize = 4 * 1024 * 1024 // 4MB default
	}
	if storeOpts.MaxConcurrentRequests == 0 {
		storeOpts.MaxConcurrentRequests = 4
	}
	if storeOpts.CacheSize == 0 {
		storeOpts.CacheSize = 8
	}
	
	// Create backing store (may do I/O - but OK here since we're NOT in FUSE callback)
	store, err := fetcher.NewBackingStore(fs.ctx, tempURL, size, storeOpts)
	if err != nil {
		log.Printf("[fs] StageFile: Failed to create backing store for %s: %v", fileName, err)
		return nil, fmt.Errorf("failed to create backing store: %w", err)
	}
	log.Printf("[fs] StageFile: Backing store created for %s (mode=%s)", fileName, storeOpts.Mode)
	
	// Now update the file with the new store
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	if exists {
		// Update existing file with new store
		existingSff.storeMu.Lock()
		if existingSff.store != nil {
			// Someone else created a store, close ours
			store.Close()
			log.Printf("[fs] StageFile: Another caller created store for %s, discarding duplicate", id)
		} else {
			existingSff.store = store
			existingSff.Status = "ready"
			log.Printf("[fs] StageFile: Updated existing file %s with new store", id)
		}
		existingSff.storeMu.Unlock()
		return existingSff.StagedFile, nil
	}
	
	// Create new staged file with store already initialized
	sf := &StagedFile{
		ID:           id,
		FileID:       fileID,
		FileName:     fileName,
		RecipientTag: recipientTag,
		Size:         size,
		ContentType:  contentType,
		ModTime:      time.Now(),
		Status:       "ready", // Store is ready!
	}
	
	sff := &stagedFileFuse{
		StagedFile: sf,
		store:      store,
		tileRef:    1, // Tile is now visible
		openRef:    0, // No FUSE handles yet
	}
	fs.stagedFiles[id] = sff
	log.Printf("[fs] StageFile: Staged new file %s with ready store (fileID=%s, recipient=%s, tileRef=1, openRef=0, total staged=%d)", 
		fileName, fileID, recipientTag, len(fs.stagedFiles))
	return sf, nil
}

func (fs *fuseFS) GetStagedFiles() []*StagedFile {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	files := make([]*StagedFile, 0, len(fs.stagedFiles))
	for _, sff := range fs.stagedFiles {
		files = append(files, sff.StagedFile)
	}
	return files
}

func (fs *fuseFS) GetFilePath(stagedFile *StagedFile) string {
	return filepath.Join(fs.mountpoint, stagedDirName, stagedFile.ID, stagedFile.FileName)
}

func (fs *fuseFS) EvictStagedFile(id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	sff, exists := fs.stagedFiles[id]
	if !exists {
		return nil
	}
	
	// Clear tileRef and try to evict
	log.Printf("EvictStagedFile: Clearing tileRef for %s", id)
	atomic.StoreInt32(&sff.tileRef, 0)
	fs.tryEvictLocked(id)
	return nil
}

func (fs *fuseFS) EvictAllStagedFiles() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	// Clear tileRef for all staged files and try to evict each
	for id, sff := range fs.stagedFiles {
		log.Printf("EvictAllStagedFiles: Clearing tileRef for %s", id)
		atomic.StoreInt32(&sff.tileRef, 0)
		fs.tryEvictLocked(id)
	}
	return nil
}

// tryEvictLocked attempts to evict a staged file if both tileRef and openRef are 0.
// Caller must hold fs.mu lock.
func (fs *fuseFS) tryEvictLocked(id string) {
	sff, exists := fs.stagedFiles[id]
	if !exists {
		return
	}
	
	tileRef := atomic.LoadInt32(&sff.tileRef)
	openRef := atomic.LoadInt32(&sff.openRef)
	
	// Only evict if both refs are 0
	if tileRef == 0 && openRef == 0 {
		log.Printf("tryEvictLocked: Both refs are 0, evicting %s", id)
		fs.doEvictLocked(id)
	} else {
		log.Printf("tryEvictLocked: Cannot evict %s yet (tileRef=%d, openRef=%d)", id, tileRef, openRef)
	}
}

// doEvictLocked unconditionally closes the BackingStore and removes the staged file.
// Caller must hold fs.mu lock.
func (fs *fuseFS) doEvictLocked(id string) {
	sff, exists := fs.stagedFiles[id]
	if !exists {
		return
	}
	
	// Close the BackingStore if it exists
	sff.storeMu.Lock()
	if sff.store != nil {
		log.Printf("doEvictLocked: Closing BackingStore for %s (refCount=%d)", id, sff.store.RefCount())
		if err := sff.store.Close(); err != nil {
			log.Printf("doEvictLocked: Error closing store for %s: %v", id, err)
		}
		sff.store = nil
	}
	sff.storeMu.Unlock()
	
	// Remove from registry
	delete(fs.stagedFiles, id)
	log.Printf("doEvictLocked: Removed %s from registry", id)
}

// FUSE operations

func (fs *fuseFS) Init() {
	defer logging.FUSETraceSimple("Init")()
	log.Println("[FUSE] Filesystem initialized")
}

func (fs *fuseFS) Destroy() {
	defer logging.FUSETraceSimple("Destroy")()
	log.Println("[FUSE] Filesystem destroyed")
}

func (fs *fuseFS) Statfs(path string, stat *fuse.Statfs_t) int {
	defer logging.FUSETraceSimple("Statfs")()
	stat.Bsize = 4096
	stat.Frsize = 4096
	stat.Blocks = 1000000
	stat.Bfree = 1000000
	stat.Bavail = 1000000
	stat.Files = 1000
	stat.Ffree = 1000
	stat.Favail = 1000
	stat.Namemax = 255
	return 0
}

func (fs *fuseFS) Access(path string, mask uint32) int {
	defer logging.FUSETrace("Access", "path=%s mask=%d", path, mask)()
	stat := &fuse.Stat_t{}
	result := fs.Getattr(path, stat, 0)
	if result != 0 {
		return result
	}
	if mask&2 != 0 || mask&1 != 0 {
		return -fuse.EACCES
	}
	return 0
}

func (fs *fuseFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	defer logging.FUSETrace("Getattr", "path=%s fh=%d", path, fh)()
	path = strings.TrimPrefix(path, "/")
	
	// Set ownership to current user (prevents root ownership and admin:/// prompts)
	stat.Uid = uint32(os.Getuid())
	stat.Gid = uint32(os.Getgid())
	
	// Root directory
	if path == "" {
		stat.Mode = fuse.S_IFDIR | 0755
		stat.Nlink = 2
		return 0
	}

	// Staged directory
	if path == stagedDirName {
		stat.Mode = fuse.S_IFDIR | 0755
		stat.Nlink = 2
		return 0
	}

	// Check if it's a staged file subdirectory or file
	if strings.HasPrefix(path, stagedDirName+"/") {
		parts := strings.Split(strings.TrimPrefix(path, stagedDirName+"/"), "/")
		
		if len(parts) == 1 {
			// Subdirectory (e.g., Staged/fileid_recipient)
			fs.mu.RLock()
			_, exists := fs.stagedFiles[parts[0]]
			fs.mu.RUnlock()
			
			if exists {
				stat.Mode = fuse.S_IFDIR | 0755
				stat.Nlink = 2
				return 0
			}
		} else if len(parts) == 2 {
			// File (e.g., Staged/fileid_recipient/filename.mp4)
			fs.mu.RLock()
			sf, exists := fs.stagedFiles[parts[0]]
			fs.mu.RUnlock()
			
			if exists && sf.FileName == parts[1] {
				stat.Mode = fuse.S_IFREG | 0644
				stat.Nlink = 1
				stat.Size = sf.Size
				stat.Mtim = fuse.NewTimespec(sf.ModTime)
				stat.Atim = stat.Mtim
				stat.Ctim = stat.Mtim
				return 0
			}
		}
	}

	return -fuse.ENOENT
}

func (fs *fuseFS) Readdir(path string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	defer logging.FUSETrace("Readdir", "path=%s ofst=%d fh=%d", path, ofst, fh)()
	path = strings.TrimPrefix(path, "/")

	// Root directory - show Staged/
	if path == "" {
		fill(".", nil, 0)
		fill("..", nil, 0)
		fill(stagedDirName, nil, 0)
		return 0
	}

	// Staged directory - show all staged file subdirectories
	if path == stagedDirName {
		fill(".", nil, 0)
		fill("..", nil, 0)
		
		fs.mu.RLock()
		for id := range fs.stagedFiles {
			fill(id, nil, 0)
		}
		fs.mu.RUnlock()
		
		return 0
	}

	// Staged file subdirectory - show the file
	if strings.HasPrefix(path, stagedDirName+"/") {
		parts := strings.Split(strings.TrimPrefix(path, stagedDirName+"/"), "/")
		
		if len(parts) == 1 {
			fs.mu.RLock()
			sf, exists := fs.stagedFiles[parts[0]]
			fs.mu.RUnlock()
			
			if exists {
				fill(".", nil, 0)
				fill("..", nil, 0)
				fill(sf.FileName, nil, 0)
				return 0
			}
		}
	}

	return -fuse.ENOENT
}

func (fs *fuseFS) Open(path string, flags int) (int, uint64) {
	defer logging.FUSETrace("Open", "path=%s flags=%d", path, flags)()
	path = strings.TrimPrefix(path, "/")
	
	// Check if it's a staged file
	if strings.HasPrefix(path, stagedDirName+"/") {
		parts := strings.Split(strings.TrimPrefix(path, stagedDirName+"/"), "/")
		
		if len(parts) == 2 {
			fs.mu.RLock()
			sf, exists := fs.stagedFiles[parts[0]]
			fs.mu.RUnlock()
			
			if exists && sf.FileName == parts[1] {
				// Check if BackingStore is ready (quick check without lock during I/O)
				sf.storeMu.Lock()
				store := sf.store
				sf.storeMu.Unlock()
				
				if store == nil {
					// Store not ready - this should not happen if StageFile() was called properly
					// Return EAGAIN to let the caller retry
					logging.FUSELog("[Open] ERROR: Backing store not ready for %s - file not properly staged", sf.FileName)
					return -fuse.EAGAIN, ^uint64(0)
				}
				
				// Check if store was evicted
				if tempStore, ok := store.(*fetcher.TempFileStore); ok && tempStore.IsEvicted() {
					logging.FUSELog("[Open] ERROR: Backing store was evicted for %s - file needs re-staging", sf.FileName)
					return -fuse.ESTALE, ^uint64(0)
				}
				
				// Increment refs and register file handle (hold locks briefly)
				sf.storeMu.Lock()
				newOpenRef := atomic.AddInt32(&sf.openRef, 1)
				store.IncRef()
				stagedID := sf.ID
				sf.Status = "open"
				sf.storeMu.Unlock()
				
				fs.fhMu.Lock()
				fh := fs.nextFH
				fs.nextFH++
				fs.fhToStore[fh] = store
				fs.fhToPath[fh] = path
				fs.fhToStagedID[fh] = stagedID
				isFirstHandle := len(fs.fhToStore) == 1
				fs.fhMu.Unlock()
				
				// Enable sleep prevention on first file handle (async to avoid blocking)
				if isFirstHandle {
					go fs.enableSleepPrevention()
				}
				
				tileRef := atomic.LoadInt32(&sf.tileRef)
				logging.FUSELog("[Open] %s opened (fh=%d, tileRef=%d, openRef=%d, storeRefCount=%d)", 
					sf.FileName, fh, tileRef, newOpenRef, store.RefCount())
				
				return 0, fh
			}
		}
	}

	return -fuse.ENOENT, ^uint64(0)
}

func (fs *fuseFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
	defer logging.FUSETrace("Read", "fh=%d off=%d len=%d", fh, ofst, len(buff))()
	
	// Get the backing store from the file handle (release lock immediately)
	fs.fhMu.RLock()
	store, exists := fs.fhToStore[fh]
	fs.fhMu.RUnlock()
	
	if !exists || store == nil {
		logging.FUSELog("[Read] Invalid file handle %d", fh)
		return -fuse.EBADF
	}

	// Perform the actual read (no locks held - this is I/O bound)
	n, err := store.ReadAt(fs.ctx, buff, ofst)
	
	// Handle EOF correctly
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0
		}
		// For other errors, only fail if we read nothing
		if n == 0 {
			logging.FUSELog("[Read] I/O error at fh=%d off=%d: %v", fh, ofst, err)
			return -fuse.EIO
		}
		// If we got some data despite error, return the data (partial read)
	}

	return n
}

func (fs *fuseFS) Release(path string, fh uint64) int {
	defer logging.FUSETrace("Release", "fh=%d", fh)()
	
	// Get and remove the file handle mapping
	fs.fhMu.Lock()
	store, exists := fs.fhToStore[fh]
	stagedID := fs.fhToStagedID[fh]
	delete(fs.fhToStore, fh)
	delete(fs.fhToPath, fh)
	delete(fs.fhToStagedID, fh)
	fs.fhMu.Unlock()
	
	if !exists || store == nil {
		logging.FUSELog("[Release] Invalid file handle %d", fh)
		return -fuse.EBADF
	}
	
	// Decrement both BackingStore refCount and openRef
	newStoreRefCount := store.DecRef()
	
	// Decrement openRef and check if we should evict
	fs.mu.RLock()
	sff, sfExists := fs.stagedFiles[stagedID]
	fs.mu.RUnlock()
	
	if sfExists {
		newOpenRef := atomic.AddInt32(&sff.openRef, -1)
		tileRef := atomic.LoadInt32(&sff.tileRef)
		logging.FUSELog("[Release] fh=%d closed (tileRef=%d, openRef=%d, storeRefCount=%d)", 
			fh, tileRef, newOpenRef, newStoreRefCount)
		
		// If both refs are 0, try to evict
		if tileRef == 0 && newOpenRef == 0 {
			logging.FUSELog("[Release] Both refs are 0 for %s, attempting eviction", stagedID)
			fs.mu.Lock()
			fs.tryEvictLocked(stagedID)
			fs.mu.Unlock()
		}
	} else {
		logging.FUSELog("[Release] fh=%d closed (storeRefCount=%d) - staged file no longer exists", 
			fh, newStoreRefCount)
	}
	
	// Disable sleep prevention if this was the last file handle (async to avoid blocking)
	go fs.disableSleepPrevention()

	return 0
}

// Read-only filesystem stubs - return appropriate errors

func (fs *fuseFS) Chmod(path string, mode uint32) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Chown(path string, uid uint32, gid uint32) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Utimens(path string, tmsp []fuse.Timespec) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Create(path string, flags int, mode uint32) (int, uint64) {
	return -fuse.EROFS, ^uint64(0)
}

func (fs *fuseFS) Mkdir(path string, mode uint32) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Unlink(path string) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Rmdir(path string) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Rename(oldpath string, newpath string) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Truncate(path string, size int64, fh uint64) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Write(path string, buff []byte, ofst int64, fh uint64) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Flush(path string, fh uint64) int {
	return 0
}

func (fs *fuseFS) Fsync(path string, datasync bool, fh uint64) int {
	return 0
}

func (fs *fuseFS) Opendir(path string) (int, uint64) {
	stat := &fuse.Stat_t{}
	result := fs.Getattr(path, stat, 0)
	if result != 0 {
		return result, ^uint64(0)
	}
	if stat.Mode&fuse.S_IFDIR == 0 {
		return -fuse.ENOTDIR, ^uint64(0)
	}
	return 0, 0
}

func (fs *fuseFS) Releasedir(path string, fh uint64) int {
	return 0
}

func (fs *fuseFS) Fsyncdir(path string, datasync bool, fh uint64) int {
	return 0
}

func (fs *fuseFS) Readlink(path string) (int, string) {
	return -fuse.ENOSYS, ""
}

func (fs *fuseFS) Symlink(target string, newpath string) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Link(oldpath string, newpath string) int {
	return -fuse.EROFS
}

func (fs *fuseFS) Setxattr(path string, name string, value []byte, flags int) int {
	return -fuse.ENOSYS
}

func (fs *fuseFS) Getxattr(path string, name string) (int, []byte) {
	return -fuse.ENOSYS, nil
}

func (fs *fuseFS) Removexattr(path string, name string) int {
	return -fuse.ENOSYS
}

func (fs *fuseFS) Listxattr(path string, fill func(name string) bool) int {
	return -fuse.ENOSYS
}

func (fs *fuseFS) Mknod(path string, mode uint32, dev uint64) int {
	return -fuse.EROFS
}

// enableSleepPrevention enables sleep prevention when the first file handle is opened.
func (fs *fuseFS) enableSleepPrevention() {
	fs.sleepMu.Lock()
	defer fs.sleepMu.Unlock()
	
	// Only enable if not already enabled
	if fs.sleepRelease != nil {
		return
	}
	
	release, err := sleep.PreventSleep()
	if err != nil {
		log.Printf("[fs] Warning: failed to enable sleep prevention: %v", err)
		return
	}
	
	fs.sleepRelease = release
}

// disableSleepPrevention disables sleep prevention when all file handles are closed.
func (fs *fuseFS) disableSleepPrevention() {
	fs.sleepMu.Lock()
	defer fs.sleepMu.Unlock()
	
	if fs.sleepRelease == nil {
		return
	}
	
	// Check if there are any open file handles
	fs.fhMu.RLock()
	hasOpenHandles := len(fs.fhToStore) > 0
	fs.fhMu.RUnlock()
	
	// Only disable if no file handles are open
	if !hasOpenHandles {
		fs.sleepRelease()
		fs.sleepRelease = nil
	}
}

// enableAppNapPrevention enables App Nap prevention while the filesystem is mounted.
// This prevents macOS from throttling the process when it loses focus (App Nap).
// App Nap is separate from system sleep and can cause FUSE operations to stall.
func (fs *fuseFS) enableAppNapPrevention() {
	fs.appNapMu.Lock()
	defer fs.appNapMu.Unlock()
	
	// Only enable if not already enabled
	if fs.appNapRelease != nil {
		return
	}
	
	release, err := appnap.PreventAppNap("Active FUSE filesystem")
	if err != nil {
		log.Printf("[fs] Warning: failed to enable App Nap prevention: %v", err)
		return
	}
	
	fs.appNapRelease = release
	log.Printf("[fs] App Nap prevention enabled (filesystem mounted)")
}

// disableAppNapPrevention disables App Nap prevention when the filesystem is unmounted.
func (fs *fuseFS) disableAppNapPrevention() {
	fs.appNapMu.Lock()
	defer fs.appNapMu.Unlock()
	
	if fs.appNapRelease == nil {
		return
	}
	
	fs.appNapRelease()
	fs.appNapRelease = nil
	log.Printf("[fs] App Nap prevention disabled (filesystem unmounted)")
}

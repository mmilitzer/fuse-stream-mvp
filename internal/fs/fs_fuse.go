//go:build fuse

package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/fetcher"
	"github.com/mmilitzer/fuse-stream-mvp/pkg/config"
	"github.com/winfsp/cgofuse/fuse"
)

const stagedDirName = "Staged"

type stagedFileFuse struct {
	*StagedFile
	store   fetcher.BackingStore
	storeMu sync.Mutex
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
	nextFH     uint64
	fhToStore  map[uint64]fetcher.BackingStore
	fhToPath   map[uint64]string
	fhMu       sync.RWMutex
}

func newFS(client *api.Client, cfg *config.Config) FS {
	ctx, cancel := context.WithCancel(context.Background())
	return &fuseFS{
		client:      client,
		config:      cfg,
		stagedFiles: make(map[string]*stagedFileFuse),
		fhToStore:   make(map[uint64]fetcher.BackingStore),
		fhToPath:    make(map[uint64]string),
		nextFH:      1,
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (fs *fuseFS) Start(opts MountOptions) error {
	fs.mountpoint = opts.Mountpoint
	fs.host = fuse.NewFileSystemHost(fs)
	
	// Mount options (OS-specific)
	mountOpts := []string{
		"-o", "ro",
		"-o", "fsname=fusestream",
	}

	switch runtime.GOOS {
	case "darwin":
		// macFUSE/macOS-specific options
		mountOpts = append(mountOpts,
			"-o", "local",
			"-o", "volname=FuseStream",
		)
	case "linux":
		// Linux: NO volname (not supported), and NO allow_other by default
		// If you need allow_other, it requires user_allow_other in /etc/fuse.conf
		// and can be gated by a config option in the future
	}

	// Mount the filesystem
	go func() {
		if !fs.host.Mount(fs.mountpoint, mountOpts) {
			log.Printf("Failed to mount filesystem at %s", fs.mountpoint)
		}
	}()

	// Wait a bit for mount to complete
	time.Sleep(100 * time.Millisecond)
	fs.mounted = true
	
	return nil
}

func (fs *fuseFS) Stop() error {
	fs.cancel()
	if fs.host != nil {
		if !fs.host.Unmount() {
			return fmt.Errorf("failed to unmount filesystem")
		}
	}
	fs.mounted = false
	return nil
}

func (fs *fuseFS) Mountpoint() string {
	return fs.mountpoint
}

func (fs *fuseFS) Mounted() bool {
	return fs.mounted
}

func (fs *fuseFS) StageFile(fileID, fileName, recipientTag string, size int64, contentType string) (*StagedFile, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	id := fmt.Sprintf("%s_%s", fileID, recipientTag)
	
	sf := &StagedFile{
		ID:           id,
		FileID:       fileID,
		FileName:     fileName,
		RecipientTag: recipientTag,
		Size:         size,
		ContentType:  contentType,
		ModTime:      time.Now(),
		Status:       "idle",
	}

	fs.stagedFiles[id] = &stagedFileFuse{
		StagedFile: sf,
	}
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

// FUSE operations

func (fs *fuseFS) Init() {
	log.Println("[FUSE] Filesystem initialized")
}

func (fs *fuseFS) Destroy() {
	log.Println("[FUSE] Filesystem destroyed")
}

func (fs *fuseFS) Statfs(path string, stat *fuse.Statfs_t) int {
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
	path = strings.TrimPrefix(path, "/")
	
	// Check if it's a staged file
	if strings.HasPrefix(path, stagedDirName+"/") {
		parts := strings.Split(strings.TrimPrefix(path, stagedDirName+"/"), "/")
		
		if len(parts) == 2 {
			fs.mu.RLock()
			sf, exists := fs.stagedFiles[parts[0]]
			fs.mu.RUnlock()
			
			if exists && sf.FileName == parts[1] {
				// Initialize BackingStore on first open
				sf.storeMu.Lock()
				if sf.store == nil {
					log.Printf("Open: Initializing backing store for %s (fileID=%s, size=%d)", sf.FileName, sf.FileID, sf.Size)
					tempURL, err := fs.client.BuildTempURL(sf.FileID, sf.RecipientTag)
					if err != nil {
						log.Printf("Failed to build temp URL for %s: %v", sf.FileName, err)
						sf.Status = "error"
						sf.storeMu.Unlock()
						return -fuse.EIO, ^uint64(0)
					}
					log.Printf("Open: Got temp URL for %s: %s", sf.FileName, tempURL)

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

					store, err := fetcher.NewBackingStore(fs.ctx, tempURL, sf.Size, storeOpts)
					if err != nil {
						log.Printf("Failed to create backing store for %s: %v", sf.FileName, err)
						sf.Status = "error"
						sf.storeMu.Unlock()
						return -fuse.EIO, ^uint64(0)
					}

					sf.store = store
					sf.Status = "open"
					log.Printf("Open: Backing store initialized successfully for %s (mode=%s)", sf.FileName, storeOpts.Mode)
				}
				sf.storeMu.Unlock()
				
				// Increment ref count and create file handle
				sf.store.IncRef()
				
				fs.fhMu.Lock()
				fh := fs.nextFH
				fs.nextFH++
				fs.fhToStore[fh] = sf.store
				fs.fhToPath[fh] = path
				fs.fhMu.Unlock()
				
				log.Printf("Open: %s opened (fh=%d, refCount=%d)", sf.FileName, fh, sf.store.RefCount())
				
				return 0, fh
			}
		}
	}

	return -fuse.ENOENT, ^uint64(0)
}

func (fs *fuseFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
	// Get the backing store from the file handle
	fs.fhMu.RLock()
	store, exists := fs.fhToStore[fh]
	filePath := fs.fhToPath[fh]
	fs.fhMu.RUnlock()
	
	if !exists || store == nil {
		log.Printf("Read: Invalid file handle %d", fh)
		return -fuse.EBADF
	}

	// Extract filename for logging
	fileName := filepath.Base(filePath)
	
	log.Printf("Read: %s reading %d bytes at offset %d (fh=%d)", fileName, len(buff), ofst, fh)
	n, err := store.ReadAt(fs.ctx, buff, ofst)
	
	// Handle EOF correctly
	if err != nil {
		if errors.Is(err, io.EOF) {
			log.Printf("Read: %s reached EOF at offset %d", fileName, ofst)
			return 0
		}
		// For other errors, only fail if we read nothing
		if n == 0 {
			log.Printf("Read error for %s at offset %d: %v", fileName, ofst, err)
			return -fuse.EIO
		}
		// If we got some data despite error, return the data
		log.Printf("Partial read for %s at offset %d: read %d bytes with error: %v", fileName, ofst, n, err)
	} else {
		log.Printf("Read: %s successfully read %d bytes at offset %d", fileName, n, ofst)
	}

	return n
}

func (fs *fuseFS) Release(path string, fh uint64) int {
	// Get and remove the file handle mapping
	fs.fhMu.Lock()
	store, exists := fs.fhToStore[fh]
	filePath := fs.fhToPath[fh]
	delete(fs.fhToStore, fh)
	delete(fs.fhToPath, fh)
	fs.fhMu.Unlock()
	
	if !exists || store == nil {
		log.Printf("Release: Invalid file handle %d", fh)
		return -fuse.EBADF
	}

	fileName := filepath.Base(filePath)
	
	// Decrement ref count
	newRefCount := store.DecRef()
	log.Printf("Release: %s closed (fh=%d, newRefCount=%d)", fileName, fh, newRefCount)
	
	// If this was the last reference, close the store
	if newRefCount == 0 {
		log.Printf("Release: Closing backing store for %s", fileName)
		if err := store.Close(); err != nil {
			log.Printf("Release: Error closing store for %s: %v", fileName, err)
		}
	}

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

package fetcher

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

const (
	// MaxTempFiles is the maximum number of temp files that can exist at once
	MaxTempFiles = 10
	
	// MaxDiskUsagePercent is the maximum percentage of disk space we can use for temp files
	MaxDiskUsagePercent = 70
	
	// TempFilePrefix is the prefix for all temp files created by this app
	TempFilePrefix = "fsmvp-"
	
	// TempFileSuffix is the suffix for all temp files created by this app
	TempFileSuffix = ".tmp"
)

// TempFileInfo tracks information about a temp file
type TempFileInfo struct {
	Path        string
	Size        int64
	LastAccess  time.Time
}

// TempFileManager manages temp files with LRU eviction and disk space limits
type TempFileManager struct {
	tempDir   string
	files     map[string]*TempFileInfo // path -> info
	mu        sync.Mutex
}

var (
	globalManager     *TempFileManager
	globalManagerOnce sync.Once
)

// GetTempFileManager returns the global singleton temp file manager
func GetTempFileManager(tempDir string) *TempFileManager {
	globalManagerOnce.Do(func() {
		if tempDir == "" {
			tempDir = os.TempDir()
		}
		globalManager = &TempFileManager{
			tempDir: tempDir,
			files:   make(map[string]*TempFileInfo),
		}
		log.Printf("[tempfile] Initialized temp file manager (tempDir=%s)", tempDir)
	})
	return globalManager
}

// CleanupStaleFiles removes all stale temp files from previous runs
func (m *TempFileManager) CleanupStaleFiles() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	pattern := filepath.Join(m.tempDir, TempFilePrefix+"*"+TempFileSuffix)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob temp files: %w", err)
	}
	
	if len(matches) == 0 {
		log.Printf("[tempfile] No stale temp files found")
		return nil
	}
	
	log.Printf("[tempfile] Found %d stale temp files from previous runs", len(matches))
	
	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			log.Printf("[tempfile] Warning: failed to remove stale temp file %s: %v", path, err)
		} else {
			log.Printf("[tempfile] Removed stale temp file: %s", path)
		}
	}
	
	return nil
}

// Register adds a temp file to the manager's tracking
func (m *TempFileManager) Register(path string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.files[path] = &TempFileInfo{
		Path:       path,
		Size:       size,
		LastAccess: time.Now(),
	}
	log.Printf("[tempfile] Registered temp file: %s (size=%d bytes)", path, size)
}

// Unregister removes a temp file from the manager's tracking
func (m *TempFileManager) Unregister(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	delete(m.files, path)
	log.Printf("[tempfile] Unregistered temp file: %s", path)
}

// UpdateAccess updates the last access time for a temp file
func (m *TempFileManager) UpdateAccess(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if info, exists := m.files[path]; exists {
		info.LastAccess = time.Now()
	}
}

// GetDiskUsage returns disk usage information for the temp directory
func (m *TempFileManager) GetDiskUsage() (total uint64, available uint64, err error) {
	// Ensure the directory exists
	if err := os.MkdirAll(m.tempDir, 0755); err != nil {
		return 0, 0, fmt.Errorf("mkdir: %w", err)
	}
	
	var stat syscall.Statfs_t
	if err := syscall.Statfs(m.tempDir, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs: %w", err)
	}
	
	// Total and available space in bytes
	total = stat.Blocks * uint64(stat.Bsize)
	available = stat.Bavail * uint64(stat.Bsize)
	
	return total, available, nil
}

// CheckSpaceAvailable checks if there's enough space for a file of the given size
// Returns true if space is available, false otherwise
func (m *TempFileManager) CheckSpaceAvailable(fileSize int64) (bool, error) {
	total, available, err := m.GetDiskUsage()
	if err != nil {
		return false, err
	}
	
	// Calculate how much space we're allowed to use (70% of total)
	maxAllowed := uint64(float64(total) * (float64(MaxDiskUsagePercent) / 100.0))
	
	// Calculate current usage
	used := total - available
	
	// Check if adding this file would exceed our limit
	newUsed := used + uint64(fileSize)
	
	log.Printf("[tempfile] Disk usage check: total=%d MB, available=%d MB, used=%d MB, maxAllowed=%d MB, fileSize=%d MB, newUsed=%d MB",
		total/(1024*1024), available/(1024*1024), used/(1024*1024), maxAllowed/(1024*1024), fileSize/(1024*1024), newUsed/(1024*1024))
	
	return newUsed <= maxAllowed, nil
}

// EnsureSpaceAvailable ensures there's enough space for a file of the given size
// by evicting old temp files if necessary. Returns error if space cannot be freed.
// IMPORTANT: This function minimizes lock holding time to avoid blocking FUSE threads.
func (m *TempFileManager) EnsureSpaceAvailable(fileSize int64) error {
	// Quick check without holding lock for long
	m.mu.Lock()
	fileCount := len(m.files)
	m.mu.Unlock()
	
	// Check disk space without holding lock (statfs can be slow)
	spaceOK, err := m.CheckSpaceAvailable(fileSize)
	if err != nil {
		return err
	}
	
	if spaceOK && fileCount < MaxTempFiles {
		log.Printf("[tempfile] Space check OK: %d files, space available for %d bytes", fileCount, fileSize)
		return nil
	}
	
	log.Printf("[tempfile] Need to free space or reduce file count (current files=%d, max=%d)", fileCount, MaxTempFiles)
	
	// Build a list of temp files to evict - hold lock only briefly
	type fileEntry struct {
		path       string
		size       int64
		lastAccess time.Time
	}
	
	m.mu.Lock()
	var entries []fileEntry
	for path, info := range m.files {
		entries = append(entries, fileEntry{
			path:       path,
			size:       info.Size,
			lastAccess: info.LastAccess,
		})
	}
	m.mu.Unlock()
	
	// Sort outside the lock
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].lastAccess.Before(entries[j].lastAccess)
	})
	
	// Try to evict files until we have enough space and are under the file limit
	var freedSpace int64
	evictedCount := 0
	
	for _, entry := range entries {
		// Check if we've freed enough space - without lock
		spaceOK, err := m.CheckSpaceAvailable(fileSize)
		if err != nil {
			return err
		}
		
		m.mu.Lock()
		currentFileCount := len(m.files)
		m.mu.Unlock()
		
		if spaceOK && currentFileCount < MaxTempFiles {
			break
		}
		
		// IMPORTANT: Do file deletion WITHOUT holding the lock
		// This prevents blocking FUSE threads during I/O
		if err := os.Remove(entry.path); err != nil {
			log.Printf("[tempfile] Warning: failed to evict temp file %s: %v", entry.path, err)
			continue
		}
		
		freedSpace += entry.size
		evictedCount++
		
		// Only hold lock briefly to update the map
		m.mu.Lock()
		delete(m.files, entry.path)
		m.mu.Unlock()
		
		log.Printf("[tempfile] Evicted temp file: %s (size=%d bytes, freed total=%d bytes)", 
			entry.path, entry.size, freedSpace)
	}
	
	// Final check: do we have enough space now?
	spaceOK, err = m.CheckSpaceAvailable(fileSize)
	if err != nil {
		return err
	}
	
	m.mu.Lock()
	finalFileCount := len(m.files)
	m.mu.Unlock()
	
	if !spaceOK {
		total, available, _ := m.GetDiskUsage()
		return fmt.Errorf("insufficient disk space: need %d MB, have %d MB available (total=%d MB, 70%% limit=%d MB)", 
			fileSize/(1024*1024), 
			available/(1024*1024),
			total/(1024*1024),
			(total*MaxDiskUsagePercent/100)/(1024*1024))
	}
	
	if finalFileCount >= MaxTempFiles {
		return fmt.Errorf("too many temp files: %d (max=%d)", finalFileCount, MaxTempFiles)
	}
	
	log.Printf("[tempfile] Space ensured: evicted %d files, freed %d bytes", evictedCount, freedSpace)
	return nil
}

// GetFileCount returns the current number of tracked temp files
func (m *TempFileManager) GetFileCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.files)
}

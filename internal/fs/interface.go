package fs

import (
	"time"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/pkg/config"
)

// StagedFile represents a file staged for upload via FUSE mount
type StagedFile struct {
	ID           string
	FileID       string
	FileName     string
	RecipientTag string
	Size         int64
	ContentType  string
	ModTime      time.Time
	Status       string // "idle", "open", "reading", "done", "error", "unavailable"
}

// MountOptions configures the FUSE mount
type MountOptions struct {
	Mountpoint string
}

// FS is the interface for filesystem operations (FUSE or stub)
type FS interface {
	// Start mounts the filesystem (FUSE) or initializes stub mode
	Start(opts MountOptions) error
	
	// Stop unmounts the filesystem or cleans up stub
	Stop() error
	
	// Mountpoint returns the configured mount path
	Mountpoint() string
	
	// Mounted returns true if FUSE is mounted, false for stub
	Mounted() bool
	
	// StageFile prepares a file for drag-and-drop upload
	StageFile(fileID, fileName, recipientTag string, size int64, contentType string) (*StagedFile, error)
	
	// GetStagedFiles returns all staged files
	GetStagedFiles() []*StagedFile
	
	// GetFilePath returns the virtual path for a staged file
	GetFilePath(sf *StagedFile) string
	
	// EvictStagedFile closes and removes a specific staged file's BackingStore.
	// This should be called when the user stages a new file or leaves the detail screen.
	EvictStagedFile(id string) error
	
	// EvictAllStagedFiles closes and removes all staged files' BackingStores.
	// This should be called on app shutdown or when clearing all staged files.
	EvictAllStagedFiles() error
	
	// HasActiveUploads returns true if any staged files have active open file handles.
	// This indicates uploads are in progress.
	HasActiveUploads() bool
}

// New creates a new FS instance (FUSE or stub based on build tags)
func New(client *api.Client, cfg *config.Config) FS {
	return newFS(client, cfg)
}

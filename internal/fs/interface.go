package fs

import (
	"time"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
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
}

// New creates a new FS instance (FUSE or stub based on build tags)
func New(client *api.Client) FS {
	return newFS(client)
}

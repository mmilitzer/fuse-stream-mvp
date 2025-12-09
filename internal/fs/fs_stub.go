//go:build !fuse

package fs

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/pkg/config"
)

type stubFS struct {
	mountpoint  string
	client      *api.Client
	stagedFiles map[string]*StagedFile
	mu          sync.RWMutex
}

func newFS(client *api.Client, cfg *config.Config) FS {
	return &stubFS{
		client:      client,
		stagedFiles: make(map[string]*StagedFile),
	}
}

func (fs *stubFS) Start(opts MountOptions) error {
	fs.mountpoint = opts.Mountpoint
	log.Printf("[STUB] FUSE support not available (build with -tags fuse). API and UI will work but filesystem features are disabled.")
	return nil
}

func (fs *stubFS) Stop() error {
	return nil
}

func (fs *stubFS) ReleaseResources() {
	// No-op for stub
}

func (fs *stubFS) Mountpoint() string {
	return fs.mountpoint
}

func (fs *stubFS) Mounted() bool {
	return false
}

func (fs *stubFS) StageFile(fileID, fileName, recipientTag string, size int64, contentType string) (*StagedFile, error) {
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
		Status:       "unavailable",
	}

	fs.stagedFiles[id] = sf
	return sf, nil
}

func (fs *stubFS) GetStagedFiles() []*StagedFile {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	files := make([]*StagedFile, 0, len(fs.stagedFiles))
	for _, sf := range fs.stagedFiles {
		files = append(files, sf)
	}
	return files
}

func (fs *stubFS) GetFilePath(sf *StagedFile) string {
	return fmt.Sprintf("(FUSE unavailable - build with -tags fuse)")
}

func (fs *stubFS) EvictStagedFile(id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.stagedFiles, id)
	return nil
}

func (fs *stubFS) EvictAllStagedFiles() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.stagedFiles = make(map[string]*StagedFile)
	return nil
}

func (fs *stubFS) HasActiveUploads() bool {
	return false
}

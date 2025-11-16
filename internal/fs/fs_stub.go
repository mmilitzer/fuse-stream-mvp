//go:build !fuse

package fs

import (
	"fmt"
	"time"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
)

type StagedFile struct {
	ID           string
	FileID       string
	FileName     string
	RecipientTag string
	Size         int64
	ContentType  string
	ModTime      time.Time
	Status       string
}

type FS struct {
	mountpoint string
	client     *api.Client
}

func New(mountpoint string, client *api.Client) *FS {
	return &FS{
		mountpoint: mountpoint,
		client:     client,
	}
}

func (fs *FS) Mount() error {
	return fmt.Errorf("FUSE support not compiled in (build with -tags fuse)")
}

func (fs *FS) Unmount() error {
	return fmt.Errorf("FUSE support not compiled in (build with -tags fuse)")
}

func (fs *FS) StageFile(fileID, fileName, recipientTag string, size int64, contentType string) (*StagedFile, error) {
	return nil, fmt.Errorf("FUSE support not compiled in (build with -tags fuse)")
}

func (fs *FS) GetStagedFiles() []*StagedFile {
	return nil
}

func (fs *FS) GetFilePath(sf *StagedFile) string {
	return ""
}

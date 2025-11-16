package ui

import (
	"context"
	"fmt"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/daemon"
	"github.com/mmilitzer/fuse-stream-mvp/internal/drag"
)

type App struct {
	ctx    context.Context
	client *api.Client
}

func NewApp(client *api.Client) *App {
	return &App{
		client: client,
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

type JobInfo struct {
	ID        string       `json:"id"`
	InputName string       `json:"inputName"`
	Outputs   []OutputInfo `json:"outputs"`
}

type OutputInfo struct {
	FileID      string `json:"fileId"`
	FileName    string `json:"fileName"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
}

func (a *App) GetJobs() ([]JobInfo, error) {
	jobs, err := a.client.ListJobs()
	if err != nil {
		return nil, err
	}

	result := make([]JobInfo, 0, len(jobs))
	for _, job := range jobs {
		outputs := make([]OutputInfo, 0, len(job.Outputs))
		for _, out := range job.Outputs {
			outputs = append(outputs, OutputInfo{
				FileID:      out.FileID,
				FileName:    out.FileName,
				Size:        out.Size,
				ContentType: out.ContentType,
			})
		}
		result = append(result, JobInfo{
			ID:        job.ID,
			InputName: job.InputName,
			Outputs:   outputs,
		})
	}
	return result, nil
}

type StageRequest struct {
	FileID       string `json:"fileId"`
	FileName     string `json:"fileName"`
	RecipientTag string `json:"recipientTag"`
	Size         int64  `json:"size"`
	ContentType  string `json:"contentType"`
}

type StageResponse struct {
	Success  bool   `json:"success"`
	FilePath string `json:"filePath"`
	Message  string `json:"message"`
}

type StagedFileInfo struct {
	ID           string `json:"id"`
	FileName     string `json:"fileName"`
	FilePath     string `json:"filePath"`
	Size         int64  `json:"size"`
	RecipientTag string `json:"recipientTag"`
	Status       string `json:"status"`
}

func (a *App) StageForUpload(req StageRequest) StageResponse {
	filesystem := daemon.GetFS()
	if filesystem == nil {
		return StageResponse{
			Success: false,
			Message: "Filesystem not available",
		}
	}

	stagedFile, err := filesystem.StageFile(req.FileID, req.FileName, req.RecipientTag, req.Size, req.ContentType)
	if err != nil {
		return StageResponse{
			Success: false,
			Message: "Failed to stage file: " + err.Error(),
		}
	}

	filePath := filesystem.GetFilePath(stagedFile)
	
	return StageResponse{
		Success:  true,
		FilePath: filePath,
		Message:  "File staged successfully",
	}
}

func (a *App) GetStagedFiles() []StagedFileInfo {
	filesystem := daemon.GetFS()
	if filesystem == nil {
		return []StagedFileInfo{}
	}

	stagedFiles := filesystem.GetStagedFiles()
	result := make([]StagedFileInfo, 0, len(stagedFiles))
	
	for _, sf := range stagedFiles {
		result = append(result, StagedFileInfo{
			ID:           sf.ID,
			FileName:     sf.FileName,
			FilePath:     filesystem.GetFilePath(sf),
			Size:         sf.Size,
			RecipientTag: sf.RecipientTag,
			Status:       sf.Status,
		})
	}
	
	return result
}

// StartNativeDrag initiates a native file drag operation (macOS only).
// Only works when the filesystem is mounted and the file exists.
func (a *App) StartNativeDrag(absPath string) error {
	filesystem := daemon.GetFS()
	if filesystem == nil {
		return fmt.Errorf("filesystem not available")
	}

	if !filesystem.Mounted() {
		return fmt.Errorf("mount disabled in this build")
	}

	return drag.StartFileDrag(absPath)
}

// EvictStagedFile explicitly removes a staged file and closes its BackingStore.
// This should be called when the user leaves the detail screen or cancels staging.
func (a *App) EvictStagedFile(id string) error {
	filesystem := daemon.GetFS()
	if filesystem == nil {
		return fmt.Errorf("filesystem not available")
	}
	return filesystem.EvictStagedFile(id)
}

// EvictAllStagedFiles removes all staged files and closes their BackingStores.
// This can be called when clearing all staged files or on app shutdown.
func (a *App) EvictAllStagedFiles() error {
	filesystem := daemon.GetFS()
	if filesystem == nil {
		return fmt.Errorf("filesystem not available")
	}
	return filesystem.EvictAllStagedFiles()
}

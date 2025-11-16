//go:build fuse

package ui

import (
	"github.com/mmilitzer/fuse-stream-mvp/internal/daemon"
)

func (a *App) StageForUpload(req StageRequest) StageResponse {
	filesystem := daemon.GetFS()
	if filesystem == nil {
		return StageResponse{
			Success: false,
			Message: "Filesystem not mounted",
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

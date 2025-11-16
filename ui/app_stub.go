//go:build !fuse

package ui

func (a *App) StageForUpload(req StageRequest) StageResponse {
	return StageResponse{
		Success: false,
		Message: "FUSE support not compiled in (build with -tags fuse)",
	}
}

func (a *App) GetStagedFiles() []StagedFileInfo {
	return []StagedFileInfo{}
}

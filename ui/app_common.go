package ui

import (
	"context"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
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

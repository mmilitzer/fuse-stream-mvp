package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Output struct {
	FileID      string
	FileName    string
	Size        int64
	ContentType string
}

type Job struct {
	ID        string
	InputName string
	Outputs   []Output
}

type Client struct {
	tokenCache *TokenCache
	apiBase    string
}

func NewClient(apiBase, clientID, clientSecret string) *Client {
	return &Client{
		tokenCache: NewTokenCache(apiBase, clientID, clientSecret),
		apiBase:    apiBase,
	}
}

type jobsResponse struct {
	Items []jobItem `json:"items"`
}

type jobItem struct {
	ID    string         `json:"id"`
	Input jobInputDetail `json:"input"`
	Files []fileDetail   `json:"files"`
}

type jobInputDetail struct {
	Name string `json:"name"`
}

type fileDetail struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

func (c *Client) ListJobs() ([]Job, error) {
	token, err := c.tokenCache.GetToken()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/jobs?expand=file", c.apiBase)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create jobs request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jobs request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jobs request failed: %d %s", resp.StatusCode, string(body))
	}

	var jr jobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		return nil, fmt.Errorf("decode jobs response: %w", err)
	}

	jobs := make([]Job, 0, len(jr.Items))
	for _, item := range jr.Items {
		job := Job{
			ID:        item.ID,
			InputName: item.Input.Name,
			Outputs:   make([]Output, 0, len(item.Files)),
		}
		for _, f := range item.Files {
			job.Outputs = append(job.Outputs, Output{
				FileID:      f.ID,
				FileName:    f.Name,
				Size:        f.Size,
				ContentType: f.ContentType,
			})
		}
		jobs = append(jobs, job)
	}

	return jobs, nil
}

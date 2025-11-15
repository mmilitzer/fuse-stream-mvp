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

func (c *Client) ListJobs() ([]Job, error) {
	token, err := c.tokenCache.GetToken()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/jobs/?status=SUCCESS&expand=file", c.apiBase)
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

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode jobs response: %w", err)
	}

	jobsArray, ok := data["jobs"].([]any)
	if !ok {
		return []Job{}, nil
	}

	jobs := make([]Job, 0, len(jobsArray))
	for _, jobData := range jobsArray {
		jobMap, ok := jobData.(map[string]any)
		if !ok {
			continue
		}

		job := Job{
			ID: getString(jobMap, "id"),
		}

		if input, ok := jobMap["input"].(map[string]any); ok {
			job.InputName = getString(input, "input_name")
		}

		if outputs, ok := jobMap["outputs"].([]any); ok {
			job.Outputs = make([]Output, 0, len(outputs))
			for _, outData := range outputs {
				outMap, ok := outData.(map[string]any)
				if !ok {
					continue
				}

				if fileMap, ok := outMap["file"].(map[string]any); ok {
					output := Output{
						FileID:      getString(fileMap, "id"),
						FileName:    getString(fileMap, "name"),
						Size:        getInt64(fileMap, "size"),
						ContentType: getString(fileMap, "mime_type"),
					}
					job.Outputs = append(job.Outputs, output)
				}
			}
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt64(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		}
	}
	return 0
}

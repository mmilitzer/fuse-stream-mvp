package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
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
	httpClient *http.Client
}

func NewClient(apiBase, clientID, clientSecret string) *Client {
	return &Client{
		tokenCache: NewTokenCache(apiBase, clientID, clientSecret),
		apiBase:    apiBase,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		},
	}
}

type ListJobsOptions struct {
	Start     int    // Offset for paging (default: 0)
	Limit     int    // Number of items per page (default: 20, max: 5000)
	Sort      string // Sort by key (e.g., "created_at")
	Direction string // Sort direction: "1" (asc) or "-1" (desc)
	NameFilter string // Filter by input.file.name (partial, case-insensitive)
}

type JobsResponse struct {
	Jobs  []Job
	Total int
}

func (c *Client) ListJobs() ([]Job, error) {
	resp, err := c.ListJobsWithOptions(ListJobsOptions{})
	if err != nil {
		return nil, err
	}
	return resp.Jobs, nil
}

func (c *Client) ListJobsWithOptions(opts ListJobsOptions) (*JobsResponse, error) {
	log.Printf("[API] ListJobsWithOptions: Getting token...")
	token, err := c.tokenCache.GetToken()
	if err != nil {
		log.Printf("[API] ListJobsWithOptions: Token error: %v", err)
		return nil, err
	}

	// Set defaults
	if opts.Limit == 0 {
		opts.Limit = 20
	}
	if opts.Sort == "" {
		opts.Sort = "created_at"
	}
	if opts.Direction == "" {
		opts.Direction = "-1" // Descending (newest first)
	}

	// Build URL with query parameters
	reqURL := fmt.Sprintf("%s/jobs/?status=SUCCESS&expand=file&start=%d&limit=%d&sort=%s&direction=%s",
		c.apiBase, opts.Start, opts.Limit, opts.Sort, opts.Direction)
	
	if opts.NameFilter != "" {
		reqURL += fmt.Sprintf("&input.file.name=%s", url.QueryEscape(opts.NameFilter))
	}

	log.Printf("[API] ListJobsWithOptions: Making request to %s", reqURL)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create jobs request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("[API] ListJobsWithOptions: Request error: %v", err)
		return nil, fmt.Errorf("jobs request: %w", err)
	}
	defer resp.Body.Close()
	log.Printf("[API] ListJobsWithOptions: Response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jobs request failed: %d %s", resp.StatusCode, string(body))
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode jobs response: %w", err)
	}

	// Extract total count if available
	total := 0
	if totalVal, ok := data["total"]; ok {
		total = int(getInt64(data, "total"))
	}

	jobsArray, ok := data["jobs"].([]any)
	if !ok {
		return &JobsResponse{Jobs: []Job{}, Total: total}, nil
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
			if fileMap, ok := input["file"].(map[string]any); ok {
				job.InputName = getString(fileMap, "name")
			}
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

	return &JobsResponse{Jobs: jobs, Total: total}, nil
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

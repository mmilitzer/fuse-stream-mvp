package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestJobsResponseParsing(t *testing.T) {
	data, err := os.ReadFile("testdata/jobs_response.json")
	if err != nil {
		t.Fatalf("Failed to read testdata: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("Failed to parse testdata: %v", err)
	}

	jobsArray, ok := response["jobs"].([]any)
	if !ok {
		t.Fatal("Expected 'jobs' array in response")
	}

	if len(jobsArray) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobsArray))
	}

	firstJob, ok := jobsArray[0].(map[string]any)
	if !ok {
		t.Fatal("First job is not a map")
	}

	if id := getString(firstJob, "id"); id != "58124aafe4b097397e2370e7" {
		t.Errorf("Expected job ID 58124aafe4b097397e2370e7, got %s", id)
	}

	input, ok := firstJob["input"].(map[string]any)
	if !ok {
		t.Fatal("input is not a map")
	}

	if inputName := getString(input, "input_name"); inputName == "" {
		t.Error("Expected non-empty input_name")
	}

	outputs, ok := firstJob["outputs"].([]any)
	if !ok {
		t.Fatal("outputs is not an array")
	}

	if len(outputs) != 1 {
		t.Errorf("Expected 1 output in first job, got %d", len(outputs))
	}

	firstOutput, ok := outputs[0].(map[string]any)
	if !ok {
		t.Fatal("First output is not a map")
	}

	fileMap, ok := firstOutput["file"].(map[string]any)
	if !ok {
		t.Fatal("file is not a map")
	}

	if fileID := getString(fileMap, "id"); fileID != "58124aafbd0740a7448cf8c2" {
		t.Errorf("Expected file ID 58124aafbd0740a7448cf8c2, got %s", fileID)
	}

	if size := getInt64(fileMap, "size"); size != 1234567 {
		t.Errorf("Expected size 1234567, got %d", size)
	}

	if mimeType := getString(fileMap, "mime_type"); mimeType != "video/mp4" {
		t.Errorf("Expected mime_type video/mp4, got %s", mimeType)
	}
}

func TestListJobsWithMockServer(t *testing.T) {
	data, err := os.ReadFile("testdata/jobs_response.json")
	if err != nil {
		t.Fatalf("Failed to read testdata: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token/" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"test_token","expires_in":3600,"token_type":"Bearer"}`))
			return
		}
		if r.URL.Path == "/jobs/" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test_token" {
				t.Errorf("Expected Bearer test_token, got %s", auth)
			}
			if r.URL.Query().Get("status") != "SUCCESS" {
				t.Error("Expected status=SUCCESS query parameter")
			}
			if r.URL.Query().Get("expand") != "file" {
				t.Error("Expected expand=file query parameter")
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test_client", "test_secret")
	jobs, err := client.ListJobs()
	if err != nil {
		t.Fatalf("Failed to list jobs: %v", err)
	}

	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobs))
	}

	job1 := jobs[0]
	if job1.ID != "58124aafe4b097397e2370e7" {
		t.Errorf("Expected job ID 58124aafe4b097397e2370e7, got %s", job1.ID)
	}
	if job1.InputName != "ED_HD.avi" {
		t.Errorf("Expected input name ED_HD.avi, got %s", job1.InputName)
	}
	if len(job1.Outputs) != 1 {
		t.Errorf("Expected 1 output, got %d", len(job1.Outputs))
	}

	out := job1.Outputs[0]
	if out.FileID != "58124aafbd0740a7448cf8c2" {
		t.Errorf("Expected file ID 58124aafbd0740a7448cf8c2, got %s", out.FileID)
	}
	if out.FileName != "ED_HD.mp4" {
		t.Errorf("Expected file name ED_HD.mp4, got %s", out.FileName)
	}
	if out.Size != 1234567 {
		t.Errorf("Expected size 1234567, got %d", out.Size)
	}
	if out.ContentType != "video/mp4" {
		t.Errorf("Expected content type video/mp4, got %s", out.ContentType)
	}

	job2 := jobs[1]
	if job2.InputName != "test-video.mov" {
		t.Errorf("Expected input name test-video.mov, got %s", job2.InputName)
	}
	if len(job2.Outputs) != 2 {
		t.Errorf("Expected 2 outputs in second job, got %d", len(job2.Outputs))
	}
}

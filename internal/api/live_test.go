package api

import (
	"os"
	"testing"
)

func TestLiveOAuthToken(t *testing.T) {
	clientID := os.Getenv("MEDIAHUB_CLIENT_ID")
	clientSecret := os.Getenv("MEDIAHUB_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		t.Skip("Skipping live test: MEDIAHUB_CLIENT_ID or MEDIAHUB_CLIENT_SECRET not set")
	}

	apiBase := "https://api.xvid.com/v1"
	cache := NewTokenCache(apiBase, clientID, clientSecret)

	token, err := cache.GetToken()
	if err != nil {
		t.Fatalf("Failed to get OAuth token: %v", err)
	}

	if token == "" {
		t.Fatal("Expected non-empty token")
	}

	t.Logf("Successfully obtained OAuth token (length: %d)", len(token))
}

func TestLiveJobsList(t *testing.T) {
	clientID := os.Getenv("MEDIAHUB_CLIENT_ID")
	clientSecret := os.Getenv("MEDIAHUB_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		t.Skip("Skipping live test: MEDIAHUB_CLIENT_ID or MEDIAHUB_CLIENT_SECRET not set")
	}

	apiBase := "https://api.xvid.com/v1"
	client := NewClient(apiBase, clientID, clientSecret)

	jobs, err := client.ListJobs()
	if err != nil {
		t.Fatalf("Failed to list jobs: %v", err)
	}

	t.Logf("Successfully fetched %d jobs with status=SUCCESS", len(jobs))

	if len(jobs) > 0 {
		job := jobs[0]
		if job.ID == "" {
			t.Error("First job has empty ID")
		}
		t.Logf("First job: ID=%s, InputName=%s, Outputs=%d", job.ID, job.InputName, len(job.Outputs))

		if len(job.Outputs) > 0 {
			out := job.Outputs[0]
			if out.FileID == "" {
				t.Error("First output has empty FileID")
			}
			if out.Size == 0 {
				t.Error("First output has zero size")
			}
			t.Logf("First output: FileID=%s, Name=%s, Size=%d, ContentType=%s",
				out.FileID, out.FileName, out.Size, out.ContentType)
		}
	}
}

func TestLiveDownloadsURL(t *testing.T) {
	clientID := os.Getenv("MEDIAHUB_CLIENT_ID")
	clientSecret := os.Getenv("MEDIAHUB_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		t.Skip("Skipping live test: MEDIAHUB_CLIENT_ID or MEDIAHUB_CLIENT_SECRET not set")
	}

	apiBase := "https://api.xvid.com/v1"
	client := NewClient(apiBase, clientID, clientSecret)

	jobs, err := client.ListJobs()
	if err != nil {
		t.Fatalf("Failed to list jobs: %v", err)
	}

	if len(jobs) == 0 {
		t.Skip("No jobs available to test downloads URL")
	}

	var fileID string
	for _, job := range jobs {
		if len(job.Outputs) > 0 {
			fileID = job.Outputs[0].FileID
			break
		}
	}

	if fileID == "" {
		t.Skip("No output files available to test downloads URL")
	}

	url, err := client.BuildTempURL(fileID, "test-ci")
	if err != nil {
		t.Fatalf("Failed to build temp URL: %v", err)
	}

	if url == "" {
		t.Fatal("Expected non-empty download URL")
	}

	t.Logf("Successfully built download URL for file %s: %s", fileID, url)
}

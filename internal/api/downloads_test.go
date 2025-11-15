package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildTempURL_RedirectFollow(t *testing.T) {
	finalURL := "https://cdn.example.com/final-download-url?token=xyz"

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"access_token":"test-token","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer tokenServer.Close()

	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Expected Bearer token, got: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Location", finalURL)
		w.WriteHeader(http.StatusFound)
	}))
	defer downloadServer.Close()

	client := NewClient(tokenServer.URL, "test-client", "test-secret")
	client.apiBase = downloadServer.URL

	url, err := client.BuildTempURL("file123", "recipient@example.com")
	if err != nil {
		t.Fatalf("BuildTempURL failed: %v", err)
	}

	if url != finalURL {
		t.Errorf("Expected final URL %s, got %s", finalURL, url)
	}
}

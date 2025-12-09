package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// redactURL masks sensitive query parameters in URLs for safe logging
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, return a generic redacted string
		return "[invalid-url]"
	}
	
	// Mask sensitive query parameters
	q := u.Query()
	sensitiveParams := []string{"access_token", "token", "api_key", "apikey", "secret", "password"}
	for _, param := range sensitiveParams {
		if q.Has(param) {
			q.Set(param, "***REDACTED***")
		}
	}
	u.RawQuery = q.Encode()
	
	return u.String()
}

func (c *Client) BuildTempURL(fileID, autographTag string) (string, error) {
	token, err := c.tokenCache.GetToken()
	if err != nil {
		log.Printf("[downloads] Failed to get OAuth token: %v", err)
		return "", err
	}

	reqURL := fmt.Sprintf("%s/files/downloads/?file_id=%s&autograph_tag=%s&redirect=true",
		c.apiBase, fileID, autographTag)
	
	log.Printf("[downloads] Building temp URL for file_id=%s, autograph_tag=%s", fileID, autographTag)

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		log.Printf("[downloads] Failed to create request: %v", err)
		return "", fmt.Errorf("create download request: %w", err)
	}
	// DO NOT log the Authorization header - it contains the OAuth bearer token
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[downloads] Request failed for URL %s: %v", redactURL(reqURL), err)
		return "", fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		// Read body for error details (but limit size to avoid memory issues)
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		bodyPreview := string(bodyBytes)
		if len(bodyBytes) == 1024 {
			bodyPreview += "...[truncated]"
		}
		
		log.Printf("[downloads] Temp URL build failed (status=%d): %s", resp.StatusCode, redactURL(reqURL))
		if bodyPreview != "" {
			log.Printf("[downloads] Error response body: %s", bodyPreview)
		}
		return "", fmt.Errorf("expected redirect, got: %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		log.Printf("[downloads] No Location header in redirect response for URL: %s", redactURL(reqURL))
		return "", fmt.Errorf("no Location header in redirect response")
	}

	log.Printf("[downloads] Successfully built temp URL (redirected to: %s)", redactURL(location))
	return location, nil
}

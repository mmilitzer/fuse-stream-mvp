package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type TokenCache struct {
	mu          sync.RWMutex
	token       string
	expiresAt   time.Time
	clientID    string
	clientSecret string
	apiBase     string
}

func NewTokenCache(apiBase, clientID, clientSecret string) *TokenCache {
	return &TokenCache{
		apiBase:      apiBase,
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

func (tc *TokenCache) GetToken() (string, error) {
	tc.mu.RLock()
	if tc.token != "" && time.Now().Before(tc.expiresAt.Add(-30*time.Second)) {
		token := tc.token
		tc.mu.RUnlock()
		return token, nil
	}
	tc.mu.RUnlock()

	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.token != "" && time.Now().Before(tc.expiresAt.Add(-30*time.Second)) {
		return tc.token, nil
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", tc.clientID)
	data.Set("client_secret", tc.clientSecret)

	tokenURL := fmt.Sprintf("%s/oauth2/token", tc.apiBase)
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed: %d %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	tc.token = tr.AccessToken
	tc.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)

	return tc.token, nil
}

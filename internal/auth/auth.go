package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	// TokenURL is the URL for the PIA token API
	TokenURL = "https://www.privateinternetaccess.com/api/client/v2/token"
	// TokenValidityDuration is how long a token is valid (24 hours)
	TokenValidityDuration = 24 * time.Hour
)

// TokenResponse represents the response from the PIA token API
type TokenResponse struct {
	Token string `json:"token"`
	Error string `json:"error"`
}

// Client handles authentication with the PIA API
type Client struct {
	httpClient *http.Client
	username   string
	password   string
	token      string
	expiresAt  time.Time
}

// NewClient creates a new authentication client
func NewClient(username, password string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		username: username,
		password: password,
	}
}

// GetToken returns a valid token, obtaining a new one if necessary
func (c *Client) GetToken() (string, error) {
	// If we have a valid token, return it
	if c.token != "" && time.Now().Before(c.expiresAt) {
		return c.token, nil
	}

	// Otherwise, get a new token
	return c.refreshToken()
}

// refreshToken obtains a new token from the PIA API
func (c *Client) refreshToken() (string, error) {
	// Create form data
	form := url.Values{}
	form.Add("username", c.username)
	form.Add("password", c.password)

	// Create request
	req, err := http.NewRequest("POST", TokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for error
	if tokenResp.Error != "" {
		return "", fmt.Errorf("API error: %s", tokenResp.Error)
	}

	// Check if token is empty
	if tokenResp.Token == "" {
		return "", fmt.Errorf("received empty token")
	}

	// Update client state
	c.token = tokenResp.Token
	c.expiresAt = time.Now().Add(TokenValidityDuration)

	return c.token, nil
}

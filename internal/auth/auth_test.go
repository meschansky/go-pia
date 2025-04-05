package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testClient is a wrapper around Client that allows us to inject a test server
type testClient struct {
	*Client
	server *httptest.Server
}

// newTestClient creates a new test client with a custom HTTP client that redirects
// requests to the test server regardless of the URL in the request
func newTestClient(server *httptest.Server, username, password string) *testClient {
	client := NewClient(username, password)
	
	// Replace the HTTP client's transport with one that redirects to our test server
	client.httpClient.Transport = &testTransport{server: server}
	
	return &testClient{
		Client: client,
		server: server,
	}
}

// testTransport is a custom http.RoundTripper that redirects all requests to the test server
type testTransport struct {
	server *httptest.Server
}

// RoundTrip implements the http.RoundTripper interface
func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace the request URL with the test server URL, but keep the path and query
	url := t.server.URL + req.URL.Path
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}
	
	// Create a new request with the same method, URL, and body
	newReq, err := http.NewRequest(req.Method, url, req.Body)
	if err != nil {
		return nil, err
	}
	
	// Copy headers
	newReq.Header = req.Header
	
	// Send the request to the test server
	return http.DefaultTransport.RoundTrip(newReq)
}

func TestGetToken(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check method and content type
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if !strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			t.Errorf("Expected Content-Type to contain application/x-www-form-urlencoded, got %s", r.Header.Get("Content-Type"))
		}
		
		// Parse form data
		r.ParseForm()
		if r.FormValue("username") != "testuser" || r.FormValue("password") != "testpass" {
			t.Errorf("Expected username=testuser and password=testpass, got username=%s and password=%s", 
				r.FormValue("username"), r.FormValue("password"))
		}
		
		// Return token response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TokenResponse{Token: "test-token"})
	}))
	defer server.Close()
	
	// Create test client
	client := newTestClient(server, "testuser", "testpass")
	
	// Get token
	token, err := client.GetToken()
	if err != nil {
		t.Fatalf("Failed to get token: %v", err)
	}
	
	// Check token
	if token != "test-token" {
		t.Errorf("Expected token to be test-token, got %s", token)
	}
	
	// Check caching
	originalExpiresAt := client.expiresAt
	
	// Get token again (should use cache)
	token2, err := client.GetToken()
	if err != nil {
		t.Fatalf("Failed to get token on second call: %v", err)
	}
	
	// Verify token and expiration time are the same
	if token2 != token {
		t.Errorf("Expected cached token to be the same")
	}
	if client.expiresAt != originalExpiresAt {
		t.Errorf("Expected expiration time to be unchanged")
	}
}

func TestRefreshToken(t *testing.T) {
	// Test cases
	testCases := []struct {
		name        string
		response    TokenResponse
		statusCode  int
		expectError bool
	}{
		{
			name:        "Valid token",
			response:    TokenResponse{Token: "test-token"},
			statusCode:  http.StatusOK,
			expectError: false,
		},
		{
			name:        "Error response",
			response:    TokenResponse{Error: "Invalid credentials"},
			statusCode:  http.StatusUnauthorized,
			expectError: true,
		},
		{
			name:        "Empty token",
			response:    TokenResponse{Token: ""},
			statusCode:  http.StatusOK,
			expectError: true,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.statusCode)
				json.NewEncoder(w).Encode(tc.response)
			}))
			defer server.Close()
			
			// Create test client
			client := newTestClient(server, "testuser", "testpass")
			
			// Force token refresh
			client.expiresAt = time.Now().Add(-1 * time.Hour)
			
			// Get token
			_, err := client.GetToken()
			
			// Check error
			if tc.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestTokenExpiration(t *testing.T) {
	// Track server calls
	callCount := 0
	
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TokenResponse{Token: "token-" + string(rune('0'+callCount))})
	}))
	defer server.Close()
	
	// Create test client
	client := newTestClient(server, "testuser", "testpass")
	
	// Get token
	token1, err := client.GetToken()
	if err != nil {
		t.Fatalf("Failed to get token: %v", err)
	}
	
	// Check token
	if token1 != "token-1" {
		t.Errorf("Expected token to be token-1, got %s", token1)
	}
	
	// Get token again (should use cache)
	token2, err := client.GetToken()
	if err != nil {
		t.Fatalf("Failed to get token on second call: %v", err)
	}
	if token2 != token1 {
		t.Errorf("Expected cached token to be the same")
	}
	
	// Expire token
	client.expiresAt = time.Now().Add(-1 * time.Hour)
	
	// Get token again (should refresh)
	token3, err := client.GetToken()
	if err != nil {
		t.Fatalf("Failed to get token after expiration: %v", err)
	}
	
	// Check new token
	if token3 != "token-2" {
		t.Errorf("Expected token to be token-2, got %s", token3)
	}
	
	// Check call count
	if callCount != 2 {
		t.Errorf("Expected 2 server calls, got %d", callCount)
	}
}

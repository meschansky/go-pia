package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	// Save original env vars
	origCredentials := os.Getenv("PIA_CREDENTIALS")
	origDebug := os.Getenv("PIA_DEBUG")
	origRefreshInterval := os.Getenv("PIA_REFRESH_INTERVAL")
	origOnPortChange := os.Getenv("PIA_ON_PORT_CHANGE")
	origScriptTimeout := os.Getenv("PIA_SCRIPT_TIMEOUT")
	origSyncScript := os.Getenv("PIA_SYNC_SCRIPT")

	// Set test env vars
	os.Setenv("PIA_CREDENTIALS", "/test/path/credentials.txt")
	os.Setenv("PIA_DEBUG", "true")
	os.Setenv("PIA_REFRESH_INTERVAL", "30m")
	os.Setenv("PIA_ON_PORT_CHANGE", "/test/script.sh")
	os.Setenv("PIA_SCRIPT_TIMEOUT", "45s")
	os.Setenv("PIA_SYNC_SCRIPT", "true")

	// Get default config
	cfg := DefaultConfig()

	// Verify values
	if cfg.CredentialsFile != "/test/path/credentials.txt" {
		t.Errorf("Expected CredentialsFile to be /test/path/credentials.txt, got %s", cfg.CredentialsFile)
	}

	if cfg.OpenVPNConfigFile != "/etc/openvpn/client/pia.ovpn" {
		t.Errorf("Expected OpenVPNConfigFile to be /etc/openvpn/client/pia.ovpn, got %s", cfg.OpenVPNConfigFile)
	}

	if cfg.CACertFile != "ca.rsa.4096.crt" {
		t.Errorf("Expected CACertFile to be ca.rsa.4096.crt, got %s", cfg.CACertFile)
	}

	if cfg.RefreshInterval != 30*time.Minute {
		t.Errorf("Expected RefreshInterval to be 30 minutes, got %s", cfg.RefreshInterval)
	}

	if !cfg.Debug {
		t.Errorf("Expected Debug to be true, got false")
	}

	if cfg.OnPortChangeScript != "/test/script.sh" {
		t.Errorf("Expected OnPortChangeScript to be /test/script.sh, got %s", cfg.OnPortChangeScript)
	}

	if cfg.ScriptTimeout != 45*time.Second {
		t.Errorf("Expected ScriptTimeout to be 45 seconds, got %s", cfg.ScriptTimeout)
	}

	if !cfg.SyncScript {
		t.Errorf("Expected SyncScript to be true, got false")
	}

	// Test with invalid duration
	os.Setenv("PIA_SCRIPT_TIMEOUT", "invalid")
	cfg = DefaultConfig()
	if cfg.ScriptTimeout != 30*time.Second {
		t.Errorf("Expected ScriptTimeout to fall back to default 30 seconds with invalid input, got %s", cfg.ScriptTimeout)
	}

	// Restore original env vars
	os.Setenv("PIA_CREDENTIALS", origCredentials)
	os.Setenv("PIA_DEBUG", origDebug)
	os.Setenv("PIA_REFRESH_INTERVAL", origRefreshInterval)
	os.Setenv("PIA_ON_PORT_CHANGE", origOnPortChange)
	os.Setenv("PIA_SCRIPT_TIMEOUT", origScriptTimeout)
	os.Setenv("PIA_SYNC_SCRIPT", origSyncScript)
}

func TestValidate(t *testing.T) {
	// Create a temporary credentials file
	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "credentials.txt")
	if err := os.WriteFile(credFile, []byte("username\npassword"), 0644); err != nil {
		t.Fatalf("Failed to create test credentials file: %v", err)
	}

	// Test cases
	testCases := []struct {
		name        string
		config      *Config
		expectError bool
	}{
		{
			name: "Valid config",
			config: &Config{
				CredentialsFile: credFile,
				OutputFile:      filepath.Join(tmpDir, "output.txt"),
			},
			expectError: false,
		},
		{
			name: "Missing credentials file",
			config: &Config{
				CredentialsFile: "",
				OutputFile:      filepath.Join(tmpDir, "output.txt"),
			},
			expectError: true,
		},
		{
			name: "Missing output file",
			config: &Config{
				CredentialsFile: credFile,
				OutputFile:      "",
			},
			expectError: true,
		},
		{
			name: "Non-existent credentials file",
			config: &Config{
				CredentialsFile: filepath.Join(tmpDir, "nonexistent.txt"),
				OutputFile:      filepath.Join(tmpDir, "output.txt"),
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestLoadCredentials(t *testing.T) {
	// Create a temporary credentials file
	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "credentials.txt")
	if err := os.WriteFile(credFile, []byte("testuser\ntestpass"), 0644); err != nil {
		t.Fatalf("Failed to create test credentials file: %v", err)
	}

	// Create config with the test credentials file
	cfg := &Config{
		CredentialsFile: credFile,
	}

	// Load credentials
	username, password, err := cfg.LoadCredentials()
	if err != nil {
		t.Fatalf("Failed to load credentials: %v", err)
	}

	// Verify credentials
	if username != "testuser" {
		t.Errorf("Expected username to be testuser, got %s", username)
	}

	if password != "testpass" {
		t.Errorf("Expected password to be testpass, got %s", password)
	}

	// Test with invalid credentials file
	invalidFile := filepath.Join(tmpDir, "invalid.txt")
	if err := os.WriteFile(invalidFile, []byte("only_username"), 0644); err != nil {
		t.Fatalf("Failed to create test invalid credentials file: %v", err)
	}

	cfg.CredentialsFile = invalidFile
	_, _, err = cfg.LoadCredentials()
	if err == nil {
		t.Errorf("Expected error for invalid credentials file but got nil")
	}
}

func TestSplitLines(t *testing.T) {
	testCases := []struct {
		input    string
		expected []string
	}{
		{
			input:    "line1\nline2\nline3",
			expected: []string{"line1", "line2", "line3"},
		},
		{
			input:    "single_line",
			expected: []string{"single_line"},
		},
		{
			input:    "",
			expected: []string{},
		},
		{
			input:    "line_with_trailing_newline\n",
			expected: []string{"line_with_trailing_newline", ""},
		},
	}

	for i, tc := range testCases {
		result := splitLines(tc.input)

		if len(result) != len(tc.expected) {
			t.Errorf("Test case %d: Expected %d lines, got %d", i, len(tc.expected), len(result))
			continue
		}

		for j, line := range result {
			if line != tc.expected[j] {
				t.Errorf("Test case %d, line %d: Expected %q, got %q", i, j, tc.expected[j], line)
			}
		}
	}
}

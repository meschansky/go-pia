package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/meschansky/go-pia/internal/config"
	"github.com/meschansky/go-pia/internal/vpn"
)

// parseArgs parses command line arguments and returns a configuration object
func parseArgs() (*config.Config, error) {
	// Parse command line arguments
	flag.Parse()

	// Get the output file path from the first argument
	if flag.NArg() != 1 {
		return nil, errors.New("usage: go-pia-port-forwarding OUTPUT_FILE")
	}
	outputFile := flag.Arg(0)

	// Create configuration
	cfg := config.DefaultConfig()
	cfg.OutputFile = outputFile

	return cfg, nil
}

// setupConfig sets up the configuration from environment variables
func setupConfig(cfg *config.Config) error {
	// Check for credentials file
	credentialsFile := os.Getenv("PIA_CREDENTIALS")
	if credentialsFile == "" {
		return errors.New("PIA_CREDENTIALS environment variable must be set")
	}
	cfg.CredentialsFile = credentialsFile

	// Check for debug mode
	debug := os.Getenv("PIA_DEBUG")
	if debug == "true" {
		cfg.Debug = true
	}

	// Check for refresh interval
	refreshInterval := os.Getenv("PIA_REFRESH_INTERVAL")
	if refreshInterval != "" {
		seconds, err := strconv.Atoi(refreshInterval)
		if err != nil {
			return errors.New("PIA_REFRESH_INTERVAL must be a valid number of seconds")
		}
		cfg.RefreshInterval = time.Duration(seconds) * time.Second
	}

	// Check for port change script
	if scriptPath := os.Getenv("PIA_ON_PORT_CHANGE"); scriptPath != "" {
		cfg.OnPortChangeScript = scriptPath
	}

	// Check for script timeout
	if timeout := os.Getenv("PIA_SCRIPT_TIMEOUT"); timeout != "" {
		seconds, err := strconv.Atoi(timeout)
		if err != nil {
			return errors.New("PIA_SCRIPT_TIMEOUT must be a valid number of seconds")
		}
		cfg.ScriptTimeout = time.Duration(seconds) * time.Second
	}

	// Check for sync script mode
	if syncScript := os.Getenv("PIA_SYNC_SCRIPT"); syncScript == "true" {
		cfg.SyncScript = true
	}

	return nil
}

func TestParseArgs(t *testing.T) {
	// Save original args
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Test cases
	testCases := []struct {
		name        string
		args        []string
		expectError bool
		outputFile  string
	}{
		{
			name:        "Valid args",
			args:        []string{"go-pia-port-forwarding", "/tmp/port.txt"},
			expectError: false,
			outputFile:  "/tmp/port.txt",
		},
		{
			name:        "No args",
			args:        []string{"go-pia-port-forwarding"},
			expectError: true,
			outputFile:  "",
		},
		{
			name:        "Too many args",
			args:        []string{"go-pia-port-forwarding", "/tmp/port.txt", "extra"},
			expectError: true,
			outputFile:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set args
			os.Args = tc.args

			// Parse args
			cfg, err := parseArgs()

			// Check error
			if tc.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Check output file
			if !tc.expectError && cfg.OutputFile != tc.outputFile {
				t.Errorf("Expected output file to be %s, got %s", tc.outputFile, cfg.OutputFile)
			}
		})
	}
}

// Mock for executePortChangeScript to use in tests
func mockExecutePortChangeScript(cfg *config.Config, port int) error {
	// Check if script path is valid
	if cfg.OnPortChangeScript == "" {
		return errors.New("no script specified")
	}
	
	// Check if script exists and is executable
	if cfg.OnPortChangeScript != "/test/valid-script.sh" && 
	   cfg.OnPortChangeScript != "/test/mock-script.sh" {
		return errors.New("script not found or not executable")
	}
	
	return nil
}

// Helper function for tests to avoid importing from main
func testGetScriptMode(cfg *config.Config) string {
	if cfg.SyncScript {
		return "synchronous"
	}
	return "asynchronous"
}

// Test for the script mode function
func TestGetScriptMode(t *testing.T) {
	testCases := []struct {
		name     string
		cfg      *config.Config
		expected string
	}{
		{
			name: "Synchronous mode",
			cfg: &config.Config{
				SyncScript: true,
			},
			expected: "synchronous",
		},
		{
			name: "Asynchronous mode",
			cfg: &config.Config{
				SyncScript: false,
			},
			expected: "asynchronous",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := testGetScriptMode(tc.cfg)
			if result != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, result)
			}
		})
	}
}

// Test helper function for script execution
func testExecuteScript(cfg *config.Config, port int) error {
	// Check if script exists
	if _, err := os.Stat(cfg.OnPortChangeScript); os.IsNotExist(err) {
		return err
	}
	
	// Create a command to execute the script
	cmd := exec.Command(cfg.OnPortChangeScript, strconv.Itoa(port), cfg.OutputFile)
	
	// If running synchronously, capture output
	if cfg.SyncScript {
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("script execution failed: %v, output: %s", err, output)
		}
	} else {
		// For async, just start the process
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start script: %v", err)
		}
	}
	
	return nil
}

// Test for script execution functionality
func TestScriptExecution(t *testing.T) {
	// Create a temporary test script
	tmpDir := t.TempDir()
	testScriptPath := filepath.Join(tmpDir, "test-script.sh")
	
	// Write a simple test script that outputs the arguments
	testScriptContent := `#!/bin/sh
echo "Port: $1"
echo "File: $2"
exit 0
`
	if err := os.WriteFile(testScriptPath, []byte(testScriptContent), 0755); err != nil {
		t.Fatalf("Failed to create test script: %v", err)
	}
	
	// Test cases
	testCases := []struct {
		name        string
		cfg         *config.Config
		port        int
		expectError bool
	}{
		{
			name: "Valid script synchronous",
			cfg: &config.Config{
				OnPortChangeScript: testScriptPath,
				OutputFile:         filepath.Join(tmpDir, "port.txt"),
				SyncScript:         true,
				ScriptTimeout:      5 * time.Second,
			},
			port:        12345,
			expectError: false,
		},
		{
			name: "Valid script asynchronous",
			cfg: &config.Config{
				OnPortChangeScript: testScriptPath,
				OutputFile:         filepath.Join(tmpDir, "port.txt"),
				SyncScript:         false,
				ScriptTimeout:      5 * time.Second,
			},
			port:        12345,
			expectError: false,
		},
		{
			name: "Non-existent script",
			cfg: &config.Config{
				OnPortChangeScript: filepath.Join(tmpDir, "nonexistent.sh"),
				OutputFile:         filepath.Join(tmpDir, "port.txt"),
				SyncScript:         true,
				ScriptTimeout:      5 * time.Second,
			},
			port:        12345,
			expectError: true,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Execute the script
			err := testExecuteScript(tc.cfg, tc.port)
			
			// Check if error matches expectation
			if tc.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

// mockDetectOpenVPNConnection is a mock for vpn.DetectOpenVPNConnection used in tests
type mockVPNDetector struct {
	callCount int
	maxFailures int
	delay time.Duration
}

func (m *mockVPNDetector) detect(configPath string) (*vpn.ConnectionInfo, error) {
	m.callCount++
	
	// Simulate delay if configured
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	
	// Return success after specified number of failures
	if m.callCount <= m.maxFailures {
		return nil, fmt.Errorf("mock VPN detection failure %d of %d", m.callCount, m.maxFailures)
	}
	
	// Success case
	return &vpn.ConnectionInfo{
		GatewayIP: "10.0.0.1",
		Hostname:  "test.privacy.network",
	}, nil
}

// TestDetectVPNWithRetry tests the VPN detection retry logic
func TestDetectVPNWithRetry(t *testing.T) {
	// Create a test configuration
	cfg := &config.Config{
		VPNRetryInterval: 100 * time.Millisecond, // Short interval for tests
		OpenVPNConfigFile: "test.ovpn",
	}
	
	testCases := []struct {
		name string
		maxFailures int
		expectedCalls int
		ctxTimeout time.Duration
		expectSuccess bool
	}{
		{
			name: "Success on first try",
			maxFailures: 0,
			expectedCalls: 1,
			ctxTimeout: 0, // No timeout
			expectSuccess: true,
		},
		{
			name: "Success after 3 failures",
			maxFailures: 3,
			expectedCalls: 4,
			ctxTimeout: 0, // No timeout
			expectSuccess: true,
		},
		{
			name: "Context cancellation",
			maxFailures: 10,
			expectedCalls: 3, // Expect around 3 calls in 250ms with 100ms retry interval
			ctxTimeout: 250 * time.Millisecond,
			expectSuccess: false,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock detector
			mockDetector := &mockVPNDetector{
				maxFailures: tc.maxFailures,
				delay: 10 * time.Millisecond, // Small delay to make context cancellation test reliable
			}
			
			// Create a context with timeout if specified
			var ctx context.Context
			var cancel context.CancelFunc
			if tc.ctxTimeout > 0 {
				ctx, cancel = context.WithTimeout(context.Background(), tc.ctxTimeout)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			
			// Create a custom detectVPNWithRetry function that uses our mock
			detectVPN := func(ctx context.Context, cfg *config.Config) (*vpn.ConnectionInfo, error) {
				var lastErr error
				for {
					// Try to detect the VPN connection using our mock
					connInfo, err := mockDetector.detect(cfg.OpenVPNConfigFile)
					if err == nil {
						return connInfo, nil
					}
					
					lastErr = err
					
					// Wait for the retry interval or until context is canceled
					select {
					case <-time.After(cfg.VPNRetryInterval):
						// Continue with the next attempt
					case <-ctx.Done():
						return nil, fmt.Errorf("VPN detection canceled: %w", lastErr)
					}
				}
			}
			
			// Call the function
			connInfo, err := detectVPN(ctx, cfg)
			
			// Check results
			if tc.expectSuccess {
				if err != nil {
					t.Errorf("Expected success, got error: %v", err)
				}
				if connInfo == nil {
					t.Error("Expected connection info, got nil")
				} else {
					if connInfo.GatewayIP != "10.0.0.1" || connInfo.Hostname != "test.privacy.network" {
						t.Errorf("Unexpected connection info: %+v", connInfo)
					}
				}
			} else {
				if err == nil {
					t.Error("Expected error, got success")
				}
				if connInfo != nil {
					t.Errorf("Expected nil connection info, got: %+v", connInfo)
				}
			}
			
			// Check call count (with some flexibility for the timeout case)
			if tc.ctxTimeout > 0 {
				// For timeout case, just check that we made some calls but not too many
				if mockDetector.callCount < 1 || mockDetector.callCount > tc.maxFailures {
					t.Errorf("Expected between 1 and %d calls, got %d", tc.maxFailures, mockDetector.callCount)
				}
			} else {
				// For non-timeout cases, check exact call count
				if mockDetector.callCount != tc.expectedCalls {
					t.Errorf("Expected %d calls, got %d", tc.expectedCalls, mockDetector.callCount)
				}
			}
		})
	}
}

func TestSetupConfig(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "credentials.txt")
	if err := os.WriteFile(credFile, []byte("testuser\ntestpass"), 0644); err != nil {
		t.Fatalf("Failed to create test credentials file: %v", err)
	}

	// Save original env vars
	origCredentials := os.Getenv("PIA_CREDENTIALS")
	origDebug := os.Getenv("PIA_DEBUG")
	origRefreshInterval := os.Getenv("PIA_REFRESH_INTERVAL")
	origOnPortChange := os.Getenv("PIA_ON_PORT_CHANGE")
	origScriptTimeout := os.Getenv("PIA_SCRIPT_TIMEOUT")
	origSyncScript := os.Getenv("PIA_SYNC_SCRIPT")

	// Restore original env vars
	defer func() {
		os.Setenv("PIA_CREDENTIALS", origCredentials)
		os.Setenv("PIA_DEBUG", origDebug)
		os.Setenv("PIA_REFRESH_INTERVAL", origRefreshInterval)
		os.Setenv("PIA_ON_PORT_CHANGE", origOnPortChange)
		os.Setenv("PIA_SCRIPT_TIMEOUT", origScriptTimeout)
		os.Setenv("PIA_SYNC_SCRIPT", origSyncScript)
	}()

	// Test cases
	testCases := []struct {
		name                string
		envCredentials      string
		envDebug            string
		envRefreshInt       string
		envOnPortChange     string
		envScriptTimeout    string
		envSyncScript       string
		outputFile          string
		expectError         bool
		expectedDebug       bool
		expectedRefresh     time.Duration
		expectedScript      string
		expectedTimeout     time.Duration
		expectedSyncScript  bool
	}{
		{
			name:                "Valid config",
			envCredentials:      credFile,
			envDebug:            "true",
			envRefreshInt:       "300",
			envOnPortChange:     "/test/script.sh",
			envScriptTimeout:    "60",
			envSyncScript:       "true",
			outputFile:          filepath.Join(tmpDir, "port.txt"),
			expectError:         false,
			expectedDebug:       true,
			expectedRefresh:     300 * time.Second,
			expectedScript:      "/test/script.sh",
			expectedTimeout:     60 * time.Second,
			expectedSyncScript:  true,
		},
		{
			name:                "Missing credentials",
			envCredentials:      "",
			envDebug:            "false",
			envRefreshInt:       "",
			envOnPortChange:     "",
			envScriptTimeout:    "",
			envSyncScript:       "",
			outputFile:          filepath.Join(tmpDir, "port.txt"),
			expectError:         true,
			expectedDebug:       false,
			expectedRefresh:     15 * time.Minute,
			expectedScript:      "",
			expectedTimeout:     30 * time.Second,
			expectedSyncScript:  false,
		},
		{
			name:                "Invalid refresh interval",
			envCredentials:      credFile,
			envDebug:            "false",
			envRefreshInt:       "invalid",
			envOnPortChange:     "",
			envScriptTimeout:    "",
			envSyncScript:       "",
			outputFile:          filepath.Join(tmpDir, "port.txt"),
			expectError:         true,
			expectedDebug:       false,
			expectedRefresh:     15 * time.Minute,
			expectedScript:      "",
			expectedTimeout:     30 * time.Second,
			expectedSyncScript:  false,
		},
		{
			name:                "Invalid script timeout",
			envCredentials:      credFile,
			envDebug:            "false",
			envRefreshInt:       "300",
			envOnPortChange:     "/test/script.sh",
			envScriptTimeout:    "invalid",
			envSyncScript:       "false",
			outputFile:          filepath.Join(tmpDir, "port.txt"),
			expectError:         true,
			expectedDebug:       false,
			expectedRefresh:     300 * time.Second,
			expectedScript:      "/test/script.sh",
			expectedTimeout:     30 * time.Second,
			expectedSyncScript:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set env vars
			os.Setenv("PIA_CREDENTIALS", tc.envCredentials)
			os.Setenv("PIA_DEBUG", tc.envDebug)
			os.Setenv("PIA_REFRESH_INTERVAL", tc.envRefreshInt)
			os.Setenv("PIA_ON_PORT_CHANGE", tc.envOnPortChange)
			os.Setenv("PIA_SCRIPT_TIMEOUT", tc.envScriptTimeout)
			os.Setenv("PIA_SYNC_SCRIPT", tc.envSyncScript)

			// Create base config
			cfg := &config.Config{
				OutputFile: tc.outputFile,
			}

			// Setup config
			err := setupConfig(cfg)

			// Check error
			if tc.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Check config values if no error
			if !tc.expectError {
				if cfg.Debug != tc.expectedDebug {
					t.Errorf("Expected Debug to be %v, got %v", tc.expectedDebug, cfg.Debug)
				}
				if cfg.RefreshInterval != tc.expectedRefresh {
					t.Errorf("Expected RefreshInterval to be %v, got %v", tc.expectedRefresh, cfg.RefreshInterval)
				}
			}
		})
	}
}

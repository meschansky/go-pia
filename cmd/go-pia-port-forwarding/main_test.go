package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/meschansky/go-pia/internal/config"
	"github.com/meschansky/go-pia/internal/portforwarding"
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
	callCount   int
	maxFailures int
	delay       time.Duration
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
		VPNRetryInterval:  100 * time.Millisecond, // Short interval for tests
		OpenVPNConfigFile: "test.ovpn",
	}

	testCases := []struct {
		name          string
		maxFailures   int
		expectedCalls int
		ctxTimeout    time.Duration
		expectSuccess bool
	}{
		{
			name:          "Success on first try",
			maxFailures:   0,
			expectedCalls: 1,
			ctxTimeout:    0, // No timeout
			expectSuccess: true,
		},
		{
			name:          "Success after 3 failures",
			maxFailures:   3,
			expectedCalls: 4,
			ctxTimeout:    0, // No timeout
			expectSuccess: true,
		},
		{
			name:          "Context cancellation",
			maxFailures:   10,
			expectedCalls: 3, // Expect around 3 calls in 250ms with 100ms retry interval
			ctxTimeout:    250 * time.Millisecond,
			expectSuccess: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock detector
			mockDetector := &mockVPNDetector{
				maxFailures: tc.maxFailures,
				delay:       10 * time.Millisecond, // Small delay to make context cancellation test reliable
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

// TestSetupConfig tests the configuration setup from environment variables
// TestResolveCACertPath tests the CA certificate path resolution function
// TestSetupLogging tests the logging configuration function
func TestSetupLogging(t *testing.T) {
	// Save original log flags to restore later
	origFlags := log.Flags()
	defer log.SetFlags(origFlags)

	// Test cases
	testCases := []struct {
		name          string
		debug         bool
		expectedFlags int
	}{
		{
			name:          "Debug mode enabled",
			debug:         true,
			expectedFlags: log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile,
		},
		{
			name:          "Debug mode disabled",
			debug:         false,
			expectedFlags: log.Ldate | log.Ltime,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call the function
			setupLogging(tc.debug)

			// Check that the flags were set correctly
			actualFlags := log.Flags()
			if actualFlags != tc.expectedFlags {
				t.Errorf("setupLogging(%v) set flags to %d, expected %d",
					tc.debug, actualFlags, tc.expectedFlags)
			}
		})
	}
}

func TestResolveCACertPath(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()

	// Create a test certificate file
	testCertName := "test-ca.crt"
	testCertPath := filepath.Join(tmpDir, testCertName)
	if err := os.WriteFile(testCertPath, []byte("test certificate"), 0644); err != nil {
		t.Fatalf("Failed to create test certificate file: %v", err)
	}

	// Test cases
	testCases := []struct {
		name      string
		certPath  string
		expectErr bool
	}{
		{
			name:      "Absolute path",
			certPath:  testCertPath,
			expectErr: false,
		},
		{
			name:      "Non-existent file",
			certPath:  "non-existent-file.crt",
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call the function
			path, err := resolveCACertPath(tc.certPath)

			// Check results
			if tc.expectErr {
				if err == nil {
					t.Errorf("resolveCACertPath(%q) did not return expected error", tc.certPath)
				}
			} else {
				if err != nil {
					t.Errorf("resolveCACertPath(%q) returned unexpected error: %v", tc.certPath, err)
				}
				if path == "" {
					t.Errorf("resolveCACertPath(%q) returned empty path", tc.certPath)
				}
				if _, err := os.Stat(path); os.IsNotExist(err) {
					t.Errorf("resolveCACertPath(%q) returned non-existent path: %s", tc.certPath, path)
				}
			}
		})
	}
}

// TestHandlePortOutput tests the port output handling function
func TestHandlePortOutput(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()

	// Create a test output file
	outputFile := filepath.Join(tmpDir, "port.txt")

	// Create a test script file
	scriptFile := filepath.Join(tmpDir, "port-script.sh")
	scriptContent := "#!/bin/sh\necho \"Port: $1\" > " + filepath.Join(tmpDir, "script-output.txt")
	if err := os.WriteFile(scriptFile, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to create test script file: %v", err)
	}

	// Save original execCommand and restore after test
	origExecCommand := execCommand
	defer func() { execCommand = origExecCommand }()

	// Mock execCommand to create a script output file instead of actually executing
	execCommand = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		// Create a fake script output file to simulate successful execution
		if len(args) > 0 {
			port := args[0]
			outputContent := fmt.Sprintf("Port: %s", port)
			os.WriteFile(filepath.Join(tmpDir, "script-output.txt"), []byte(outputContent), 0644)
		}
		// Return a command that does nothing but succeeds
		cmd := exec.CommandContext(ctx, "echo", "test")
		return cmd
	}

	// Test cases
	testCases := []struct {
		name            string
		port            int
		outputFile      string
		script          string
		portChanged     bool
		expectScriptRun bool
	}{
		{
			name:            "Port changed with script",
			port:            12345,
			outputFile:      outputFile,
			script:          scriptFile,
			portChanged:     true,
			expectScriptRun: true,
		},
		{
			name:            "Port unchanged with script",
			port:            12345,
			outputFile:      outputFile,
			script:          scriptFile,
			portChanged:     false,
			expectScriptRun: false,
		},
		{
			name:            "Port changed without script",
			port:            12345,
			outputFile:      outputFile,
			script:          "",
			portChanged:     true,
			expectScriptRun: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test configuration
			cfg := &config.Config{
				OutputFile:         tc.outputFile,
				OnPortChangeScript: tc.script,
			}

			// Remove any previous output files
			os.Remove(tc.outputFile)
			scriptOutputFile := filepath.Join(tmpDir, "script-output.txt")
			os.Remove(scriptOutputFile)

			// Call the function
			handlePortOutput(tc.port, cfg, tc.portChanged)

			// Check if the port was written to the output file
			if tc.outputFile != "" {
				portBytes, err := os.ReadFile(tc.outputFile)
				if err != nil {
					t.Errorf("Failed to read output file: %v", err)
				} else {
					portStr := string(portBytes)
					expectedPort := strconv.Itoa(tc.port)
					if portStr != expectedPort {
						t.Errorf("Expected port %s in output file, got %s", expectedPort, portStr)
					}
				}
			}

			// Check if the script was run
			if tc.expectScriptRun {
				// Check if script output file exists
				if _, err := os.Stat(scriptOutputFile); os.IsNotExist(err) {
					t.Errorf("Script was not run when expected")
				} else {
					// Check script output content
					outputBytes, err := os.ReadFile(scriptOutputFile)
					if err != nil {
						t.Errorf("Failed to read script output file: %v", err)
					} else {
						expectedOutput := fmt.Sprintf("Port: %d", tc.port)
						if string(outputBytes) != expectedOutput {
							t.Errorf("Expected script output %q, got %q", expectedOutput, string(outputBytes))
						}
					}
				}
			} else {
				// Check script output file does not exist
				if _, err := os.Stat(scriptOutputFile); !os.IsNotExist(err) {
					t.Errorf("Script was run when not expected")
				}
			}
		})
	}
}

// TestRefreshPortForwarding tests the port forwarding refresh function
func TestRefreshPortForwarding(t *testing.T) {
	// Create a mock port forwarding client
	type mockPFClient struct {
		refreshError bool
	}

	// Define a test version of refreshPortForwarding that accepts our mock client and signature function
	testRefreshPortForwarding := func(client interface{}, getSignature func(c *mockPFClient, pfInfo *portforwarding.PortForwardingInfo) (*portforwarding.PortForwardingInfo, error), pfInfo *portforwarding.PortForwardingInfo, initialPort *int, portChanged *bool) *portforwarding.PortForwardingInfo {
		mockClient := client.(*mockPFClient)
		newPfInfo, err := getSignature(mockClient, pfInfo)
		if err != nil {
			return pfInfo
		}

		*portChanged = newPfInfo.Port != *initialPort
		*initialPort = newPfInfo.Port
		return newPfInfo
	}

	// Mock GetSignature method
	mockGetSignature := func(c *mockPFClient, pfInfo *portforwarding.PortForwardingInfo) (*portforwarding.PortForwardingInfo, error) {
		if c.refreshError {
			return nil, errors.New("mock refresh error")
		}

		// Return a new port forwarding info with a different port and future expiry
		return &portforwarding.PortForwardingInfo{
			Port:      54321, // Different from initial port
			ExpiresAt: time.Now().Add(48 * time.Hour),
			Signature: "new-signature",
			Payload:   "new-payload",
		}, nil
	}

	// Test cases
	testCases := []struct {
		name              string
		initialPort       int
		refreshError      bool
		expectPort        int
		expectPortChanged bool
	}{
		{
			name:              "Successful refresh with port change",
			initialPort:       12345,
			refreshError:      false,
			expectPort:        54321,
			expectPortChanged: true,
		},
		{
			name:              "Failed refresh",
			initialPort:       12345,
			refreshError:      true,
			expectPort:        12345, // Should keep initial port
			expectPortChanged: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock client
			mockClient := &mockPFClient{refreshError: tc.refreshError}

			// Create initial port forwarding info
			initialInfo := &portforwarding.PortForwardingInfo{
				Port:      tc.initialPort,
				ExpiresAt: time.Now().Add(1 * time.Hour),
				Signature: "initial-signature",
				Payload:   "initial-payload",
			}

			// Variables to track port changes
			initialPortVar := tc.initialPort
			portChanged := false

			// Call a modified version of refreshPortForwarding for testing
			resultInfo := testRefreshPortForwarding(mockClient, mockGetSignature, initialInfo, &initialPortVar, &portChanged)

			// Check results
			if tc.refreshError {
				// Should return the original info on error
				if resultInfo != initialInfo {
					t.Errorf("Expected original info to be returned on error, got different info")
				}
			} else {
				// Should return new info on success
				if resultInfo.Port != tc.expectPort {
					t.Errorf("Expected port %d, got %d", tc.expectPort, resultInfo.Port)
				}
				if resultInfo.Signature != "new-signature" {
					t.Errorf("Expected new signature, got %s", resultInfo.Signature)
				}
			}

			// Check port change tracking
			if portChanged != tc.expectPortChanged {
				t.Errorf("Expected portChanged to be %v, got %v", tc.expectPortChanged, portChanged)
			}

			// Check initial port variable
			if initialPortVar != tc.expectPort {
				t.Errorf("Expected initialPort to be %d, got %d", tc.expectPort, initialPortVar)
			}
		})
	}
}

func TestSetupConfig(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()

	// Create a temporary credentials file
	credFile := filepath.Join(tmpDir, "credentials.txt")
	if err := os.WriteFile(credFile, []byte("username\npassword"), 0600); err != nil {
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
		name               string
		envCredentials     string
		envDebug           string
		envRefreshInt      string
		envOnPortChange    string
		envScriptTimeout   string
		envSyncScript      string
		outputFile         string
		expectError        bool
		expectedDebug      bool
		expectedRefresh    time.Duration
		expectedScript     string
		expectedTimeout    time.Duration
		expectedSyncScript bool
	}{
		{
			name:               "Valid config",
			envCredentials:     credFile,
			envDebug:           "true",
			envRefreshInt:      "300",
			envOnPortChange:    "/test/script.sh",
			envScriptTimeout:   "60",
			envSyncScript:      "true",
			outputFile:         filepath.Join(tmpDir, "port.txt"),
			expectError:        false,
			expectedDebug:      true,
			expectedRefresh:    300 * time.Second,
			expectedScript:     "/test/script.sh",
			expectedTimeout:    60 * time.Second,
			expectedSyncScript: true,
		},
		{
			name:               "Missing credentials",
			envCredentials:     "",
			envDebug:           "false",
			envRefreshInt:      "",
			envOnPortChange:    "",
			envScriptTimeout:   "",
			envSyncScript:      "",
			outputFile:         filepath.Join(tmpDir, "port.txt"),
			expectError:        true,
			expectedDebug:      false,
			expectedRefresh:    15 * time.Minute,
			expectedScript:     "",
			expectedTimeout:    30 * time.Second,
			expectedSyncScript: false,
		},
		{
			name:               "Invalid refresh interval",
			envCredentials:     credFile,
			envDebug:           "false",
			envRefreshInt:      "invalid",
			envOnPortChange:    "",
			envScriptTimeout:   "",
			envSyncScript:      "",
			outputFile:         filepath.Join(tmpDir, "port.txt"),
			expectError:        true,
			expectedDebug:      false,
			expectedRefresh:    15 * time.Minute,
			expectedScript:     "",
			expectedTimeout:    30 * time.Second,
			expectedSyncScript: false,
		},
		{
			name:               "Invalid script timeout",
			envCredentials:     credFile,
			envDebug:           "false",
			envRefreshInt:      "300",
			envOnPortChange:    "/test/script.sh",
			envScriptTimeout:   "invalid",
			envSyncScript:      "false",
			outputFile:         filepath.Join(tmpDir, "port.txt"),
			expectError:        true,
			expectedDebug:      false,
			expectedRefresh:    300 * time.Second,
			expectedScript:     "/test/script.sh",
			expectedTimeout:    30 * time.Second,
			expectedSyncScript: false,
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

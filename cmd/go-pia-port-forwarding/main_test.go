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

func (m *mockVPNDetector) detect(ctx context.Context, configPath string) (*vpn.ConnectionInfo, error) {
	m.callCount++

	// Simulate delay, honoring cancellation.
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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

			// Call the real production function with the injected detector.
			connInfo, err := detectVPNWithRetry(ctx, cfg.VPNRetryInterval, cfg.OpenVPNConfigFile, mockDetector.detect)

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

// fakePFClient implements portForwardingClient for tests. It records calls and
// delegates to optional function fields.
type fakePFClient struct {
	getFunc   func() (*portforwarding.PortForwardingInfo, error)
	bindFunc  func(payload, signature string) error
	getCalls  int
	bindCalls int
}

func (f *fakePFClient) GetPortForwarding() (*portforwarding.PortForwardingInfo, error) {
	f.getCalls++
	if f.getFunc != nil {
		return f.getFunc()
	}
	return nil, errors.New("GetPortForwarding not implemented")
}

func (f *fakePFClient) BindPort(payload, signature string) error {
	f.bindCalls++
	if f.bindFunc != nil {
		return f.bindFunc(payload, signature)
	}
	return nil
}

// TestRefreshPortForwarding tests the real refresh function with a fake client.
func TestRefreshPortForwarding(t *testing.T) {
	testCases := []struct {
		name              string
		initialPort       int
		refreshError      bool
		newPort           int
		expectPort        int
		expectPortChanged bool
	}{
		{
			name:              "Successful refresh with port change",
			initialPort:       12345,
			newPort:           54321,
			expectPort:        54321,
			expectPortChanged: true,
		},
		{
			name:              "Successful refresh without port change",
			initialPort:       12345,
			newPort:           12345,
			expectPort:        12345,
			expectPortChanged: false,
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
			client := &fakePFClient{}
			if tc.refreshError {
				client.getFunc = func() (*portforwarding.PortForwardingInfo, error) {
					return nil, errors.New("mock refresh error")
				}
			} else {
				client.getFunc = func() (*portforwarding.PortForwardingInfo, error) {
					return &portforwarding.PortForwardingInfo{
						Port:      tc.newPort,
						ExpiresAt: time.Now().Add(48 * time.Hour),
						Signature: "new-signature",
						Payload:   "new-payload",
					}, nil
				}
			}

			initialInfo := &portforwarding.PortForwardingInfo{
				Port:      tc.initialPort,
				ExpiresAt: time.Now().Add(1 * time.Hour),
				Signature: "initial-signature",
				Payload:   "initial-payload",
			}

			resultInfo, portChanged := refreshPortForwarding(client, initialInfo, tc.initialPort)

			if tc.refreshError {
				if resultInfo != initialInfo {
					t.Error("Expected original info to be returned on error")
				}
			} else if resultInfo.Port != tc.expectPort {
				t.Errorf("Expected port %d, got %d", tc.expectPort, resultInfo.Port)
			}

			if portChanged != tc.expectPortChanged {
				t.Errorf("Expected portChanged to be %v, got %v", tc.expectPortChanged, portChanged)
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

// writeTestScript creates an executable shell script in dir and returns its
// path. The script body is prepended with a shebang.
func writeTestScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatalf("failed to write test script: %v", err)
	}
	return path
}

// TestExecutePortChangeScriptAsyncNotCanceled is a regression test: the
// spawning function must not cancel the context while an async script is still
// running, otherwise exec.CommandContext kills it immediately.
func TestExecutePortChangeScriptAsyncNotCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "marker")
	script := writeTestScript(t, tmpDir, "slow.sh", fmt.Sprintf("sleep 0.4\nprintf '%%s' \"$1\" > %q\n", marker))

	cfg := &config.Config{
		OnPortChangeScript: script,
		OutputFile:         filepath.Join(tmpDir, "port.txt"),
		SyncScript:         false,
		ScriptTimeout:      5 * time.Second,
	}

	executePortChangeScript(cfg, 12345)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && string(b) == "12345" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("async script was canceled before completing")
}

// TestExecutePortChangeScriptSyncTimeout verifies synchronous scripts honor the
// configured timeout instead of blocking forever.
func TestExecutePortChangeScriptSyncTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	script := writeTestScript(t, tmpDir, "hang.sh", "sleep 10\n")

	cfg := &config.Config{
		OnPortChangeScript: script,
		OutputFile:         filepath.Join(tmpDir, "port.txt"),
		SyncScript:         true,
		ScriptTimeout:      200 * time.Millisecond,
	}

	start := time.Now()
	executePortChangeScript(cfg, 1)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("sync script did not respect timeout, took %s", elapsed)
	}
}

func TestRetryGetToken(t *testing.T) {
	calls := 0
	getToken := func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("temporary auth failure")
		}
		return "token-ok", nil
	}

	token, err := retryGetToken(context.Background(), 5*time.Millisecond, getToken)
	if err != nil {
		t.Fatalf("retryGetToken: %v", err)
	}
	if token != "token-ok" {
		t.Errorf("token = %q, want token-ok", token)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}

	// Cancellation must abort the retry loop.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := retryGetToken(ctx, 5*time.Millisecond, func() (string, error) {
		return "", errors.New("nope")
	}); err == nil {
		t.Error("expected error after context cancellation")
	}
}

// TestRunPortForwardingLoopSuccess covers the happy path: the loop obtains a
// port, binds it, writes the output file and runs the port-change script.
func TestRunPortForwardingLoopSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "port.txt")
	marker := filepath.Join(tmpDir, "script-ran")
	// Sync script so the marker is written before the test inspects it.
	script := writeTestScript(t, tmpDir, "onchange.sh", fmt.Sprintf("printf '%%s' \"$1\" > %q\n", marker))

	cfg := &config.Config{
		OutputFile:         outputFile,
		OnPortChangeScript: script,
		SyncScript:         true,
		ScriptTimeout:      5 * time.Second,
		RefreshInterval:    20 * time.Millisecond,
		VPNRetryInterval:   5 * time.Millisecond,
	}

	client := &fakePFClient{
		getFunc: func() (*portforwarding.PortForwardingInfo, error) {
			return &portforwarding.PortForwardingInfo{
				Port:      12345,
				ExpiresAt: time.Now().Add(48 * time.Hour),
				Payload:   "payload",
				Signature: "signature",
			}, nil
		},
	}
	connect := func(ctx context.Context) (portForwardingClient, error) { return client, nil }

	ctx, cancel := context.WithCancel(context.Background())
	refreshed := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPortForwardingLoop(ctx, cfg, connect, refreshed)
	}()

	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for initial refresh")
	}

	if b, err := os.ReadFile(outputFile); err != nil || string(b) != "12345" {
		t.Errorf("output file = %q, err=%v; want 12345", string(b), err)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "12345" {
		t.Errorf("script marker = %q, err=%v; want 12345", string(b), err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop after context cancellation")
	}
	if client.bindCalls < 1 {
		t.Errorf("bindCalls = %d, want >= 1", client.bindCalls)
	}
}

// TestRunPortForwardingLoopRetriesInitialFailure verifies the loop retries a
// failing initial connection and request instead of giving up.
func TestRunPortForwardingLoopRetriesInitialFailure(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		OutputFile:       filepath.Join(tmpDir, "port.txt"),
		RefreshInterval:  20 * time.Millisecond,
		VPNRetryInterval: 5 * time.Millisecond,
	}

	client := &fakePFClient{}
	getCalls := 0
	client.getFunc = func() (*portforwarding.PortForwardingInfo, error) {
		getCalls++
		if getCalls < 3 {
			return nil, errors.New("temporary API failure")
		}
		return &portforwarding.PortForwardingInfo{
			Port:      40000,
			ExpiresAt: time.Now().Add(48 * time.Hour),
			Payload:   "p",
			Signature: "s",
		}, nil
	}

	connectCalls := 0
	connect := func(ctx context.Context) (portForwardingClient, error) {
		connectCalls++
		if connectCalls < 2 {
			return nil, errors.New("VPN not ready")
		}
		return client, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	refreshed := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPortForwardingLoop(ctx, cfg, connect, refreshed)
	}()

	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for initial refresh after retries")
	}

	if connectCalls < 2 {
		t.Errorf("connectCalls = %d, want >= 2", connectCalls)
	}
	if getCalls < 3 {
		t.Errorf("getCalls = %d, want >= 3", getCalls)
	}

	cancel()
	<-done
}

// TestRunPortForwardingLoopReconnectsAfterBindFailure verifies that a bind
// failure triggers re-detection/reconnection and a fresh signature, and that a
// new port is propagated to the output file and script.
func TestRunPortForwardingLoopReconnectsAfterBindFailure(t *testing.T) {
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "port.txt")

	cfg := &config.Config{
		OutputFile:       outputFile,
		RefreshInterval:  10 * time.Millisecond,
		VPNRetryInterval: 5 * time.Millisecond,
	}

	first := &fakePFClient{
		getFunc: func() (*portforwarding.PortForwardingInfo, error) {
			return &portforwarding.PortForwardingInfo{Port: 11111, ExpiresAt: time.Now().Add(48 * time.Hour), Payload: "p1", Signature: "s1"}, nil
		},
		bindFunc: func(payload, signature string) error {
			return errors.New("tunnel down")
		},
	}
	second := &fakePFClient{
		getFunc: func() (*portforwarding.PortForwardingInfo, error) {
			return &portforwarding.PortForwardingInfo{Port: 22222, ExpiresAt: time.Now().Add(48 * time.Hour), Payload: "p2", Signature: "s2"}, nil
		},
	}

	connectCalls := 0
	connect := func(ctx context.Context) (portForwardingClient, error) {
		connectCalls++
		if connectCalls == 1 {
			return first, nil
		}
		return second, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	refreshed := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPortForwardingLoop(ctx, cfg, connect, refreshed)
	}()

	<-refreshed

	// Wait until the reconnect path has produced the new port in the file.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(outputFile); err == nil && string(b) == "22222" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if b, err := os.ReadFile(outputFile); err != nil || string(b) != "22222" {
		t.Errorf("output file = %q, err=%v; want 22222 after reconnect", string(b), err)
	}
	if connectCalls < 2 {
		t.Errorf("connectCalls = %d, want >= 2", connectCalls)
	}

	cancel()
	<-done
}

func TestWaitForInitialization(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		refreshed := make(chan struct{}, 1)
		refreshed <- struct{}{}
		done := make(chan struct{})
		if err := waitForInitialization(context.Background(), refreshed, done, func() {}, time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("ctx canceled stops and waits", func(t *testing.T) {
		ctx, cancelCtx := context.WithCancel(context.Background())
		cancelCtx()
		done := make(chan struct{})
		canceled := false
		cancel := func() { canceled = true; close(done) }
		if err := waitForInitialization(ctx, make(chan struct{}), done, cancel, time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !canceled {
			t.Error("expected loop cancel to be called")
		}
	})

	t.Run("timeout cancels and waits for done", func(t *testing.T) {
		done := make(chan struct{})
		waited := false
		cancel := func() { close(done) }
		err := waitForInitialization(context.Background(), make(chan struct{}), done, cancel, 20*time.Millisecond)
		if err == nil {
			t.Fatal("expected timeout error")
		}
		select {
		case <-done:
			waited = true
		default:
		}
		if !waited {
			t.Error("expected waitForInitialization to block until loop exited")
		}
	})
}

// TestRunPortForwardingLoopNoFalseReadiness verifies the loop does not signal
// readiness while the initial bind keeps failing.
func TestRunPortForwardingLoopNoFalseReadiness(t *testing.T) {
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "port.txt")

	cfg := &config.Config{
		OutputFile:       outputFile,
		RefreshInterval:  time.Hour,
		VPNRetryInterval: 10 * time.Millisecond,
	}

	client := &fakePFClient{
		getFunc: func() (*portforwarding.PortForwardingInfo, error) {
			return &portforwarding.PortForwardingInfo{Port: 12345, ExpiresAt: time.Now().Add(48 * time.Hour), Payload: "p", Signature: "s"}, nil
		},
		bindFunc: func(payload, signature string) error { return errors.New("bind down") },
	}
	connect := func(ctx context.Context) (portForwardingClient, error) { return client, nil }

	ctx, cancel := context.WithCancel(context.Background())
	refreshed := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() { defer close(done); runPortForwardingLoop(ctx, cfg, connect, refreshed) }()

	select {
	case <-refreshed:
		cancel()
		t.Fatal("readiness signaled while initial bind was failing")
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(outputFile); !os.IsNotExist(err) {
		t.Errorf("output file should not exist before a successful bind, stat err=%v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop")
	}

	if client.bindCalls < 2 {
		t.Errorf("expected repeated bind attempts, got %d", client.bindCalls)
	}
}

// TestRunPortForwardingLoopPromptReconnect verifies that a bind failure triggers
// an immediate reconnect (not a full RefreshInterval wait) and that the new
// gateway's port is bound and published.
func TestRunPortForwardingLoopPromptReconnect(t *testing.T) {
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "port.txt")

	// Refresh interval is huge: only the reconnect path can make progress.
	cfg := &config.Config{
		OutputFile:       outputFile,
		RefreshInterval:  time.Hour,
		VPNRetryInterval: 10 * time.Millisecond,
	}

	first := &fakePFClient{
		getFunc: func() (*portforwarding.PortForwardingInfo, error) {
			return &portforwarding.PortForwardingInfo{Port: 11111, ExpiresAt: time.Now().Add(48 * time.Hour), Payload: "p1", Signature: "s1"}, nil
		},
		bindFunc: func(payload, signature string) error { return errors.New("old gateway") },
	}
	second := &fakePFClient{
		getFunc: func() (*portforwarding.PortForwardingInfo, error) {
			return &portforwarding.PortForwardingInfo{Port: 22222, ExpiresAt: time.Now().Add(48 * time.Hour), Payload: "p2", Signature: "s2"}, nil
		},
	}

	connectCalls := 0
	connect := func(ctx context.Context) (portForwardingClient, error) {
		connectCalls++
		if connectCalls == 1 {
			return first, nil
		}
		return second, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	refreshed := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() { defer close(done); runPortForwardingLoop(ctx, cfg, connect, refreshed) }()

	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out: reconnect did not publish promptly")
	}

	if b, err := os.ReadFile(outputFile); err != nil || string(b) != "22222" {
		t.Errorf("output = %q, err=%v; want 22222", string(b), err)
	}
	cancel()
	<-done
}

// TestRunPortForwardingLoopWaitsForSuccessfulPublish verifies that a failed
// output write delays readiness and preserves portChanged so the change script
// runs only once publishing succeeds.
func TestRunPortForwardingLoopWaitsForSuccessfulPublish(t *testing.T) {
	tmpDir := t.TempDir()
	// `blocker` is a file, so MkdirAll(blocker) fails until we remove it.
	blocker := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	outputFile := filepath.Join(blocker, "port.txt")
	marker := filepath.Join(tmpDir, "script-ran")
	script := writeTestScript(t, tmpDir, "onchange.sh", fmt.Sprintf("printf '%%s' \"$1\" > %q\n", marker))

	cfg := &config.Config{
		OutputFile:         outputFile,
		OnPortChangeScript: script,
		SyncScript:         true,
		ScriptTimeout:      5 * time.Second,
		RefreshInterval:    time.Hour,
		VPNRetryInterval:   10 * time.Millisecond,
	}

	client := &fakePFClient{
		getFunc: func() (*portforwarding.PortForwardingInfo, error) {
			return &portforwarding.PortForwardingInfo{Port: 33333, ExpiresAt: time.Now().Add(48 * time.Hour), Payload: "p", Signature: "s"}, nil
		},
	}
	connect := func(ctx context.Context) (portForwardingClient, error) { return client, nil }

	ctx, cancel := context.WithCancel(context.Background())
	refreshed := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() { defer close(done); runPortForwardingLoop(ctx, cfg, connect, refreshed) }()

	// Publishing cannot succeed yet, so readiness must not be signaled.
	select {
	case <-refreshed:
		cancel()
		t.Fatal("readiness signaled despite failed output write")
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("script must not run before successful publish, stat err=%v", err)
	}

	// Remove the blocking file; the loop must recover and publish.
	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}

	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for publish after output path became writable")
	}

	if b, err := os.ReadFile(outputFile); err != nil || string(b) != "33333" {
		t.Errorf("output = %q, err=%v; want 33333", string(b), err)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "33333" {
		t.Errorf("script marker = %q, err=%v; want 33333 (portChanged not preserved)", string(b), err)
	}
	cancel()
	<-done
}

func TestBackoffBounded(t *testing.T) {
	b := newBackoff(60 * time.Second)
	if got := b.next(); got != time.Second {
		t.Errorf("first backoff = %s, want 1s", got)
	}
	if got := b.next(); got != 2*time.Second {
		t.Errorf("second backoff = %s, want 2s", got)
	}
	// Saturates at the cap and never decreases.
	for i := 0; i < 20; i++ {
		if got := b.next(); got > 60*time.Second {
			t.Fatalf("backoff exceeded cap: %s", got)
		}
	}

	// A cap smaller than one second is used as the base to keep tests fast.
	tiny := newBackoff(5 * time.Millisecond)
	if got := tiny.next(); got != 5*time.Millisecond {
		t.Errorf("tiny backoff = %s, want 5ms", got)
	}
}

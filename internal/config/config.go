package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config holds the application configuration
type Config struct {
	// Path to the file containing PIA credentials (username and password)
	CredentialsFile string
	// Path to the file where the forwarded port will be written
	OutputFile string
	// Path to the OpenVPN configuration file
	OpenVPNConfigFile string
	// Path to the CA certificate file
	CACertFile string
	// Refresh interval for port forwarding (in seconds)
	RefreshInterval time.Duration
	// Enable debug logging
	Debug bool
	// Path to script to execute when port changes
	OnPortChangeScript string
	// Whether to run the script synchronously (wait for completion)
	SyncScript bool
	// Timeout for script execution (in seconds)
	ScriptTimeout time.Duration
	// Retry interval for VPN connection attempts (in seconds)
	VPNRetryInterval time.Duration
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	// Parse refresh interval from environment if set
	refreshInterval := 15 * time.Minute
	if refreshStr := os.Getenv("PIA_REFRESH_INTERVAL"); refreshStr != "" {
		if refreshSec, err := time.ParseDuration(refreshStr); err == nil {
			refreshInterval = refreshSec
		}
	}

	// Parse script timeout from environment if set
	scriptTimeout := 30 * time.Second
	if timeoutStr := os.Getenv("PIA_SCRIPT_TIMEOUT"); timeoutStr != "" {
		if timeout, err := time.ParseDuration(timeoutStr); err == nil {
			scriptTimeout = timeout
		}
	}

	// Parse VPN retry interval from environment if set
	vpnRetryInterval := 60 * time.Second
	if retryStr := os.Getenv("PIA_VPN_RETRY_INTERVAL"); retryStr != "" {
		if retry, err := time.ParseDuration(retryStr); err == nil {
			vpnRetryInterval = retry
		}
	}

	return &Config{
		CredentialsFile:    os.Getenv("PIA_CREDENTIALS"),
		OpenVPNConfigFile:  "/etc/openvpn/client/pia.ovpn",
		CACertFile:         "ca.rsa.4096.crt", // Will look for this in the current directory
		RefreshInterval:    refreshInterval,
		Debug:              os.Getenv("PIA_DEBUG") == "true",
		OnPortChangeScript: os.Getenv("PIA_ON_PORT_CHANGE"),
		SyncScript:         os.Getenv("PIA_SYNC_SCRIPT") == "true",
		ScriptTimeout:      scriptTimeout,
		VPNRetryInterval:   vpnRetryInterval,
	}
}

// SetupFlags registers command line flags for all configuration options
func SetupFlags(cfg *Config) {
	// Define command line flags for all configuration options
	flag.StringVar(&cfg.CredentialsFile, "credentials", cfg.CredentialsFile, "Path to the file containing PIA credentials (username and password)")

	flag.StringVar(&cfg.OpenVPNConfigFile, "openvpn-config", cfg.OpenVPNConfigFile, "Path to the OpenVPN configuration file")

	flag.StringVar(&cfg.CACertFile, "ca-cert", cfg.CACertFile, "Path to the CA certificate file")

	// Use a string variable for duration flags, will be parsed after flag.Parse()
	refreshIntervalStr := flag.String("refresh-interval", "", "Refresh interval for port forwarding (e.g., 15m, 900s)")

	scriptTimeoutStr := flag.String("script-timeout", "", "Timeout for script execution (e.g., 30s, 1m)")

	vpnRetryIntervalStr := flag.String("vpn-retry-interval", "", "Retry interval for VPN connection attempts (e.g., 60s, 1m)")

	flag.BoolVar(&cfg.Debug, "debug", cfg.Debug, "Enable debug logging")

	flag.StringVar(&cfg.OnPortChangeScript, "on-port-change", cfg.OnPortChangeScript, "Script to execute when port changes")

	flag.BoolVar(&cfg.SyncScript, "sync-script", cfg.SyncScript, "Whether to run the script synchronously (wait for completion)")

	// Parse the flags
	flag.Parse()

	// Get the output file from the first non-flag argument
	if flag.NArg() > 0 {
		cfg.OutputFile = flag.Arg(0)
	}

	// Parse duration flags if provided
	if *refreshIntervalStr != "" {
		if d, err := time.ParseDuration(*refreshIntervalStr); err == nil {
			cfg.RefreshInterval = d
		}
	}

	if *scriptTimeoutStr != "" {
		if d, err := time.ParseDuration(*scriptTimeoutStr); err == nil {
			cfg.ScriptTimeout = d
		}
	}

	if *vpnRetryIntervalStr != "" {
		if d, err := time.ParseDuration(*vpnRetryIntervalStr); err == nil {
			cfg.VPNRetryInterval = d
		}
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.CredentialsFile == "" {
		return fmt.Errorf("credentials file path is required (set PIA_CREDENTIALS environment variable)")
	}

	if c.OutputFile == "" {
		return fmt.Errorf("output file path is required (provide as first argument)")
	}

	// Check if credentials file exists
	if _, err := os.Stat(c.CredentialsFile); os.IsNotExist(err) {
		return fmt.Errorf("credentials file does not exist: %s", c.CredentialsFile)
	}

	// Ensure the output file directory exists
	outputDir := filepath.Dir(c.OutputFile)
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	return nil
}

// LoadCredentials loads the PIA credentials from the credentials file
func (c *Config) LoadCredentials() (username, password string, err error) {
	data, err := os.ReadFile(c.CredentialsFile)
	if err != nil {
		return "", "", fmt.Errorf("failed to read credentials file: %w", err)
	}

	lines := splitLines(string(data))
	if len(lines) < 2 {
		return "", "", fmt.Errorf("invalid credentials file format: expected at least 2 lines")
	}

	return lines[0], lines[1], nil
}

// Helper function to split a string into lines
func splitLines(s string) []string {
	var lines []string
	var line string

	for _, r := range s {
		switch r {
		case '\r':
			// ignore carriage returns to support Windows line endings
			continue
		case '\n':
			lines = append(lines, line)
			line = ""
		default:
			line += string(r)
		}
	}

	// Add the last line if there's any content or if the string ended with a newline
	// and we've already added all previous content
	if line != "" || (len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r')) {
		lines = append(lines, line)
	}

	return lines
}

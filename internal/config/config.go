package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config holds the application configuration
type Config struct {
	parseErr error
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
	refreshInterval, refreshErr := envDuration("PIA_REFRESH_INTERVAL", 15*time.Minute)
	scriptTimeout, scriptErr := envDuration("PIA_SCRIPT_TIMEOUT", 30*time.Second)
	vpnRetryInterval, retryErr := envDuration("PIA_VPN_RETRY_INTERVAL", time.Minute)

	caCertFile := "ca.rsa.4096.crt"
	if caCert := os.Getenv("PIA_CA_CERT"); caCert != "" {
		caCertFile = caCert
	}

	return &Config{
		parseErr:           firstError(refreshErr, scriptErr, retryErr),
		CredentialsFile:    os.Getenv("PIA_CREDENTIALS"),
		OpenVPNConfigFile:  "/etc/openvpn/client/pia.ovpn",
		CACertFile:         caCertFile,
		RefreshInterval:    refreshInterval,
		Debug:              os.Getenv("PIA_DEBUG") == "true",
		OnPortChangeScript: os.Getenv("PIA_ON_PORT_CHANGE"),
		SyncScript:         os.Getenv("PIA_SYNC_SCRIPT") == "true",
		ScriptTimeout:      scriptTimeout,
		VPNRetryInterval:   vpnRetryInterval,
	}
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	d, err := parsePositiveDuration(name, value)
	if err != nil {
		return fallback, err
	}
	return d, nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// SetupFlags registers command line flags for all configuration options. It
// returns an error if a duration flag cannot be parsed or is not positive.
func SetupFlags(cfg *Config) error {
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
		d, err := parsePositiveDuration("refresh-interval", *refreshIntervalStr)
		if err != nil {
			return err
		}
		cfg.RefreshInterval = d
	}

	if *scriptTimeoutStr != "" {
		d, err := parsePositiveDuration("script-timeout", *scriptTimeoutStr)
		if err != nil {
			return err
		}
		cfg.ScriptTimeout = d
	}

	if *vpnRetryIntervalStr != "" {
		d, err := parsePositiveDuration("vpn-retry-interval", *vpnRetryIntervalStr)
		if err != nil {
			return err
		}
		cfg.VPNRetryInterval = d
	}

	return nil
}

// parsePositiveDuration parses a duration flag value and rejects zero or
// negative durations, which would otherwise produce a busy loop or a ticker
// panic at runtime.
func parsePositiveDuration(name, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be greater than zero", name, value)
	}
	return d, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.parseErr != nil {
		return c.parseErr
	}
	if c.CredentialsFile == "" {
		return fmt.Errorf("credentials file path is required (set PIA_CREDENTIALS environment variable)")
	}

	if c.OutputFile == "" {
		return fmt.Errorf("output file path is required (provide as first argument)")
	}

	if c.RefreshInterval <= 0 {
		return fmt.Errorf("refresh interval must be greater than zero, got %s", c.RefreshInterval)
	}

	if c.ScriptTimeout <= 0 {
		return fmt.Errorf("script timeout must be greater than zero, got %s", c.ScriptTimeout)
	}

	if c.VPNRetryInterval <= 0 {
		return fmt.Errorf("VPN retry interval must be greater than zero, got %s", c.VPNRetryInterval)
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

	username = strings.TrimSpace(lines[0])
	password = lines[1]
	if username == "" {
		return "", "", fmt.Errorf("invalid credentials file: username is empty")
	}
	if password == "" {
		return "", "", fmt.Errorf("invalid credentials file: password is empty")
	}

	return username, password, nil
}

// Helper function to split a string into lines
func splitLines(s string) []string {
	if s == "" {
		return nil
	}

	// Ignore carriage returns to support Windows line endings, then split on newlines.
	return strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
}

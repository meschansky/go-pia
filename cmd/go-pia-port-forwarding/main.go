package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/meschansky/go-pia/internal/auth"
	"github.com/meschansky/go-pia/internal/config"
	"github.com/meschansky/go-pia/internal/portforwarding"
	"github.com/meschansky/go-pia/internal/vpn"
)

// Mock the exec.CommandContext function for testing
var execCommand = exec.CommandContext

// getScriptMode returns a string describing the script execution mode
func getScriptMode(cfg *config.Config) string {
	if cfg.SyncScript {
		return "synchronous"
	}
	return "asynchronous"
}

// executePortChangeScript runs the configured script when the port changes
func executePortChangeScript(cfg *config.Config, port int) {
	log.Printf("Executing port change script: %s", cfg.OnPortChangeScript)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ScriptTimeout)
	defer cancel()

	// Create the command using the execCommand variable for better testability
	cmd := execCommand(ctx, cfg.OnPortChangeScript, strconv.Itoa(port), cfg.OutputFile)

	// If running synchronously, capture output
	if cfg.SyncScript {
		// Capture output
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("Script execution failed: %v\nOutput: %s", err, string(output))
		} else {
			log.Printf("Script executed successfully\nOutput: %s", string(output))
		}
	} else {
		// Run asynchronously with proper process detachment
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
			Pgid:    0,
		}

		if err := cmd.Start(); err != nil {
			log.Printf("Failed to start script: %v", err)
		} else {
			log.Printf("Started script asynchronously (pid: %d)", cmd.Process.Pid)

			// Start a goroutine to log when the process completes
			go func() {
				err := cmd.Wait()
				if err != nil {
					log.Printf("Async script execution failed (pid: %d): %v", cmd.Process.Pid, err)
				} else {
					log.Printf("Async script execution completed successfully (pid: %d)", cmd.Process.Pid)
				}
			}()
		}
	}
}

// detectVPNWithRetry attempts to detect an OpenVPN connection with retries
func detectVPNWithRetry(ctx context.Context, cfg *config.Config) (*vpn.ConnectionInfo, error) {
	var lastErr error
	for {
		// Try to detect the VPN connection
		connInfo, err := vpn.DetectOpenVPNConnection(cfg.OpenVPNConfigFile)
		if err == nil {
			return connInfo, nil
		}

		lastErr = err
		log.Printf("Failed to detect OpenVPN connection: %v. Retrying in %s...", err, cfg.VPNRetryInterval)

		// Wait for the retry interval or until context is canceled
		select {
		case <-time.After(cfg.VPNRetryInterval):
			// Continue with the next attempt
		case <-ctx.Done():
			return nil, fmt.Errorf("VPN detection canceled: %w", lastErr)
		}
	}
}

// setupLogging configures the logging based on debug mode
func setupLogging(debug bool) {
	if debug {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.Ldate | log.Ltime)
	}
}

// logConfigInfo logs the configuration information
func logConfigInfo(cfg *config.Config) {
	log.Printf("Starting PIA port forwarding service")
	log.Printf("Credentials file: %s", cfg.CredentialsFile)
	log.Printf("Output file: %s", cfg.OutputFile)
	log.Printf("OpenVPN config file: %s", cfg.OpenVPNConfigFile)
	log.Printf("Refresh interval: %s", cfg.RefreshInterval)
	log.Printf("VPN retry interval: %s", cfg.VPNRetryInterval)

	if cfg.OnPortChangeScript != "" {
		log.Printf("Port change script: %s", cfg.OnPortChangeScript)
		log.Printf("Script execution mode: %s", getScriptMode(cfg))
		log.Printf("Script timeout: %s", cfg.ScriptTimeout)
	}
}

// getAuthToken obtains a PIA authentication token
func getAuthToken(cfg *config.Config) (string, error) {
	// Load credentials
	username, password, err := cfg.LoadCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}

	// Create authentication client
	authClient := auth.NewClient(username, password)

	// Get token
	log.Printf("Obtaining PIA authentication token...")
	token, err := authClient.GetToken()
	if err != nil {
		return "", fmt.Errorf("failed to get token: %w", err)
	}
	log.Printf("Successfully obtained PIA token")

	return token, nil
}

// setupSignalHandler sets up a channel for OS signals
func setupSignalHandler() chan os.Signal {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	return sigChan
}

// resolveCACertPath resolves the CA certificate path
func resolveCACertPath(certPath string) (string, error) {
	if filepath.IsAbs(certPath) {
		return certPath, nil
	}

	// If it's not an absolute path, look for it in the current directory
	localPath := filepath.Join(".", certPath)

	// Check if the file exists
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	// If not, try to find it in the same directory as the examples
	examplesPath := filepath.Join("/etc/openvpn/client", certPath)
	if _, err := os.Stat(examplesPath); err == nil {
		return examplesPath, nil
	}

	return "", fmt.Errorf("CA certificate file not found: %s", certPath)
}

// runPortForwardingLoop handles the port forwarding refresh loop
func runPortForwardingLoop(pfClient *portforwarding.Client, cfg *config.Config, sigChan chan os.Signal, refreshed chan struct{}) {
	// Create a ticker for refreshing the port forwarding
	ticker := time.NewTicker(cfg.RefreshInterval)
	defer ticker.Stop()

	// Get initial port forwarding info - this will be reused until it expires
	var pfInfo *portforwarding.PortForwardingInfo
	var err error

	// Get the initial port forwarding info
	pfInfo, err = pfClient.GetPortForwarding()
	if err != nil {
		log.Printf("Failed to get initial port forwarding info: %v", err)
		return
	}

	log.Printf("Obtained port forwarding: port=%d, expires=%s", pfInfo.Port, pfInfo.ExpiresAt)

	// Store the initial port for change detection
	initialPort := pfInfo.Port
	portChanged := true // Set to true for initial execution

	for {
		// Check if we need to get a new signature (if close to expiration)
		if time.Until(pfInfo.ExpiresAt) < 24*time.Hour {
			pfInfo = refreshPortForwarding(pfClient, pfInfo, &initialPort, &portChanged)
		}

		// Bind the port
		if err := pfClient.BindPort(pfInfo.Payload, pfInfo.Signature); err != nil {
			log.Printf("Failed to bind port: %v", err)
			// Wait for the next tick
			select {
			case <-ticker.C:
				continue
			case <-sigChan:
				return
			}
		}

		log.Printf("Successfully bound port %d", pfInfo.Port)

		// Handle port file writing and script execution
		handlePortOutput(pfInfo.Port, cfg, portChanged)
		portChanged = false // Reset the flag after executing the script

		// Signal that the port forwarding has been refreshed
		select {
		case refreshed <- struct{}{}:
		default:
		}

		// Wait for the next tick
		select {
		case <-ticker.C:
		case <-sigChan:
			return
		}
	}
}

// refreshPortForwarding gets a new port forwarding signature when needed
func refreshPortForwarding(pfClient *portforwarding.Client, pfInfo *portforwarding.PortForwardingInfo, initialPort *int, portChanged *bool) *portforwarding.PortForwardingInfo {
	log.Printf("Port forwarding signature expiring soon, requesting a new one")
	newPfInfo, err := pfClient.GetPortForwarding()
	if err != nil {
		log.Printf("Failed to get new port forwarding info: %v", err)
		return pfInfo
	}

	*portChanged = newPfInfo.Port != *initialPort
	*initialPort = newPfInfo.Port
	log.Printf("Obtained new port forwarding: port=%d, expires=%s", newPfInfo.Port, newPfInfo.ExpiresAt)
	return newPfInfo
}

// handlePortOutput writes the port to file and executes script if needed
func handlePortOutput(port int, cfg *config.Config, portChanged bool) {
	// Write the port to the output file
	if err := portforwarding.WritePortToFile(port, cfg.OutputFile); err != nil {
		log.Printf("Failed to write port to file: %v", err)
		return
	}

	log.Printf("Wrote port %d to file: %s", port, cfg.OutputFile)

	// Execute port change script if configured, but only if the port has changed
	if cfg.OnPortChangeScript != "" && portChanged {
		log.Printf("Port changed, executing script")
		executePortChangeScript(cfg, port)
	}
}

func main() {
	// Create a default configuration
	cfg := config.DefaultConfig()

	// Setup and parse command line flags
	config.SetupFlags(cfg)

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Set up logging
	setupLogging(cfg.Debug)

	// Log configuration information
	logConfigInfo(cfg)

	// Get authentication token
	token, err := getAuthToken(cfg)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// Set up signal handling for graceful shutdown
	sigChan := setupSignalHandler()

	// Detect OpenVPN connection with retry logic
	log.Printf("Detecting OpenVPN connection...")

	// Create a context that can be canceled on SIGINT/SIGTERM
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	// Setup a goroutine to handle signals and cancel the context
	go func() {
		<-sigChan
		log.Println("Received termination signal, stopping VPN detection...")
		cancelCtx()
		// Re-send the signal to ensure clean termination after context is canceled
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		p, _ := os.FindProcess(os.Getpid())
		p.Signal(syscall.SIGTERM)
	}()

	// Try to detect the VPN connection, with retries
	connInfo, err := detectVPNWithRetry(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to detect OpenVPN connection after retries: %v", err)
	}
	log.Printf("Detected OpenVPN connection: gateway=%s, hostname=%s", connInfo.GatewayIP, connInfo.Hostname)

	// Reset the signal handler for the main loop
	signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Resolve CA certificate path
	caCertPath, err := resolveCACertPath(cfg.CACertFile)
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("Using CA certificate: %s", caCertPath)

	// Create port forwarding client
	pfClient := portforwarding.NewClient(token, connInfo.GatewayIP, connInfo.Hostname, caCertPath)

	// Create a channel to signal when the port forwarding is refreshed
	refreshed := make(chan struct{})

	// Start the port forwarding refresh loop in a goroutine
	go runPortForwardingLoop(pfClient, cfg, sigChan, refreshed)

	// Wait for the first port forwarding refresh
	select {
	case <-refreshed:
		log.Printf("Port forwarding initialized successfully")
	case <-time.After(30 * time.Second):
		log.Fatalf("Timed out waiting for port forwarding initialization")
	case <-sigChan:
		log.Printf("Received signal, shutting down...")
		return
	}

	// Wait for a signal to shut down
	<-sigChan
	log.Printf("Received signal, shutting down...")
}

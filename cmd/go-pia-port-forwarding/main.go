package main

import (
	"context"
	"flag"
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

// Define command line flags
var onPortChangeScript = flag.String("on-port-change", "", "Script to execute when port changes")

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

func main() {
	// Parse command line arguments
	flag.Parse()

	// Get the output file path from the first argument
	var outputFile string
	if flag.NArg() > 0 {
		outputFile = flag.Arg(0)
	}

	// Load configuration
	cfg := config.DefaultConfig()
	cfg.OutputFile = outputFile
	
	// If on-port-change script is specified via command line flag, it takes precedence
	if *onPortChangeScript != "" {
		cfg.OnPortChangeScript = *onPortChangeScript
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Set up logging
	if cfg.Debug {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.Ldate | log.Ltime)
	}

	log.Printf("Starting PIA port forwarding service")
	log.Printf("Credentials file: %s", cfg.CredentialsFile)
	log.Printf("Output file: %s", cfg.OutputFile)
	log.Printf("OpenVPN config file: %s", cfg.OpenVPNConfigFile)
	log.Printf("Refresh interval: %s", cfg.RefreshInterval)

	if cfg.OnPortChangeScript != "" {
		log.Printf("Port change script: %s", cfg.OnPortChangeScript)
		log.Printf("Script execution mode: %s", getScriptMode(cfg))
		log.Printf("Script timeout: %s", cfg.ScriptTimeout)
	}

	// Load credentials
	username, password, err := cfg.LoadCredentials()
	if err != nil {
		log.Fatalf("Failed to load credentials: %v", err)
	}

	// Create authentication client
	authClient := auth.NewClient(username, password)

	// Get token
	log.Printf("Obtaining PIA authentication token...")
	token, err := authClient.GetToken()
	if err != nil {
		log.Fatalf("Failed to get token: %v", err)
	}
	log.Printf("Successfully obtained PIA token")

	// Detect OpenVPN connection
	log.Printf("Detecting OpenVPN connection...")
	connInfo, err := vpn.DetectOpenVPNConnection(cfg.OpenVPNConfigFile)
	if err != nil {
		log.Fatalf("Failed to detect OpenVPN connection: %v", err)
	}
	log.Printf("Detected OpenVPN connection: gateway=%s, hostname=%s", connInfo.GatewayIP, connInfo.Hostname)

	// Resolve CA certificate path
	caCertPath := cfg.CACertFile
	if !filepath.IsAbs(caCertPath) {
		// If it's not an absolute path, look for it in the current directory
		caCertPath = filepath.Join(".", caCertPath)
		// Check if the file exists
		if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
			// If not, try to find it in the same directory as the examples
			examplesPath := filepath.Join("/etc/openvpn/client", caCertPath)
			if _, err := os.Stat(examplesPath); err == nil {
				caCertPath = examplesPath
			} else {
				log.Fatalf("CA certificate file not found: %s", cfg.CACertFile)
			}
		}
	}
	log.Printf("Using CA certificate: %s", caCertPath)

	// Create port forwarding client
	pfClient := portforwarding.NewClient(token, connInfo.GatewayIP, connInfo.Hostname, caCertPath)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create a ticker for refreshing the port forwarding
	ticker := time.NewTicker(cfg.RefreshInterval)
	defer ticker.Stop()

	// Create a channel to signal when the port forwarding is refreshed
	refreshed := make(chan struct{})

	// Start the port forwarding refresh loop in a goroutine
	go func() {
		for {
			// Get port forwarding info
			pfInfo, err := pfClient.GetPortForwarding()
			if err != nil {
				log.Printf("Failed to get port forwarding info: %v", err)
				// Wait for the next tick
				select {
				case <-ticker.C:
					continue
				case <-sigChan:
					return
				}
			}

			log.Printf("Obtained port forwarding: port=%d, expires=%s", pfInfo.Port, pfInfo.ExpiresAt)

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

			// Write the port to the output file
			if err := portforwarding.WritePortToFile(pfInfo.Port, cfg.OutputFile); err != nil {
				log.Printf("Failed to write port to file: %v", err)
			} else {
				log.Printf("Wrote port %d to file: %s", pfInfo.Port, cfg.OutputFile)

				// Execute port change script if configured
				if cfg.OnPortChangeScript != "" {
					executePortChangeScript(cfg, pfInfo.Port)
				}
			}

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
	}()

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

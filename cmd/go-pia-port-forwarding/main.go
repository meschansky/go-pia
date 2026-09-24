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

const (
	// initializationTimeout bounds how long we wait for the first port binding.
	initializationTimeout = 30 * time.Second
	// defaultScriptTimeout is used if no positive timeout is configured.
	defaultScriptTimeout = 30 * time.Second
)

// execCommand is the executor used to run port change scripts. It is a
// variable so tests can substitute a fake.
var execCommand = exec.CommandContext

// vpnDetector detects an active OpenVPN connection. It honors ctx.
type vpnDetector func(ctx context.Context, configPath string) (*vpn.ConnectionInfo, error)

// portForwardingClient is the subset of the port forwarding client used by the
// service loop.
type portForwardingClient interface {
	GetPortForwarding() (*portforwarding.PortForwardingInfo, error)
	BindPort(payload, signature string) error
}

// clientConnector establishes a (possibly new) port forwarding client,
// re-detecting the VPN connection and rebuilding the client as needed.
type clientConnector func(ctx context.Context) (portForwardingClient, error)

// getScriptMode returns a string describing the script execution mode
func getScriptMode(cfg *config.Config) string {
	if cfg.SyncScript {
		return "synchronous"
	}
	return "asynchronous"
}

// executePortChangeScript runs the configured script when the port changes.
//
// In asynchronous mode the configured timeout still applies, but the context
// is only canceled once the process has been reaped. Canceling it earlier (as
// a defer in the spawning function would) would immediately kill the child.
// A synchronous script failure is returned so callers can retry; asynchronous
// scripts report their own result via logs.
func executePortChangeScript(cfg *config.Config, port int) error {
	log.Printf("Executing port change script: %s", cfg.OnPortChangeScript)

	timeout := cfg.ScriptTimeout
	if timeout <= 0 {
		timeout = defaultScriptTimeout
	}

	if cfg.SyncScript {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cmd := execCommand(ctx, cfg.OnPortChangeScript, strconv.Itoa(port), cfg.OutputFile)
		configureScriptCommand(cmd)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("Script execution failed: %v\nOutput: %s", err, string(output))
			return fmt.Errorf("script %q failed: %w", cfg.OnPortChangeScript, err)
		}
		log.Printf("Script executed successfully\nOutput: %s", string(output))
		return nil
	}

	// Asynchronous execution. Keep the timeout context alive until the process
	// finishes so the child is not killed the moment this function returns.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := execCommand(ctx, cfg.OnPortChangeScript, strconv.Itoa(port), cfg.OutputFile)
	cmd.Stdout = nil
	cmd.Stderr = nil
	configureScriptCommand(cmd)

	if err := cmd.Start(); err != nil {
		cancel()
		log.Printf("Failed to start script: %v", err)
		return fmt.Errorf("failed to start script %q: %w", cfg.OnPortChangeScript, err)
	}

	log.Printf("Started script asynchronously (pid: %d)", cmd.Process.Pid)
	go func() {
		defer cancel()
		if err := cmd.Wait(); err != nil {
			log.Printf("Async script execution failed (pid: %d): %v", cmd.Process.Pid, err)
		} else {
			log.Printf("Async script execution completed successfully (pid: %d)", cmd.Process.Pid)
		}
	}()
	return nil
}

// configureScriptCommand isolates the script in its own process group and
// bounds how long Wait can block.
//
// We deliberately do NOT override exec.CommandContext's Cancel: a custom Cancel
// that calls syscall.Kill(-pid, ...) can fire after the process has already
// exited (while watchCtx is winding down) and then kill an unrelated process
// that reused the pid/process group. The default Cancel kills the script
// process; WaitDelay ensures Wait still returns promptly when a grandchild
// holds the inherited stdout/stderr pipes open.
func configureScriptCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}
	cmd.WaitDelay = 500 * time.Millisecond
}

// detectVPNWithRetry attempts to detect an OpenVPN connection with retries
func detectVPNWithRetry(ctx context.Context, retryInterval time.Duration, configPath string, detect vpnDetector) (*vpn.ConnectionInfo, error) {
	if retryInterval <= 0 {
		retryInterval = time.Minute
	}

	var lastErr error
	for {
		connInfo, err := detect(ctx, configPath)
		if err == nil {
			return connInfo, nil
		}

		lastErr = err
		log.Printf("Failed to detect OpenVPN connection: %v. Retrying in %s...", err, retryInterval)

		if !waitForRetry(ctx, retryInterval) {
			return nil, fmt.Errorf("VPN detection canceled: %w", lastErr)
		}
	}
}

// retryGetToken obtains a PIA authentication token with retry logic
func retryGetToken(ctx context.Context, retryInterval time.Duration, getToken func() (string, error)) (string, error) {
	if retryInterval <= 0 {
		retryInterval = time.Minute
	}

	var lastErr error
	for {
		log.Printf("Obtaining PIA authentication token...")
		token, err := getToken()
		if err == nil {
			log.Printf("Successfully obtained PIA token")
			return token, nil
		}

		lastErr = err
		log.Printf("Failed to get authentication token: %v. Retrying in %s...", err, retryInterval)

		if !waitForRetry(ctx, retryInterval) {
			return "", fmt.Errorf("authentication canceled: %w", lastErr)
		}
	}
}

// waitForRetry waits for the retry interval, returning false if the context was
// canceled first.
func waitForRetry(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
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
	log.Printf("CA certificate: %s", cfg.CACertFile)
	log.Printf("Refresh interval: %s", cfg.RefreshInterval)
	log.Printf("VPN retry interval: %s", cfg.VPNRetryInterval)

	if cfg.OnPortChangeScript != "" {
		log.Printf("Port change script: %s", cfg.OnPortChangeScript)
		log.Printf("Script execution mode: %s", getScriptMode(cfg))
		log.Printf("Script timeout: %s", cfg.ScriptTimeout)
	}
}

// resolveCACertPath resolves the CA certificate path, searching well-known
// locations for a relative path.
func resolveCACertPath(certPath string) (string, error) {
	if certPath == "" {
		certPath = "ca.rsa.4096.crt"
	}

	var candidates []string
	if filepath.IsAbs(certPath) {
		candidates = append(candidates, certPath)
	} else {
		candidates = append(candidates, filepath.Join(".", certPath))
		if exePath, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exePath), certPath))
		}
		candidates = append(candidates,
			filepath.Join("/etc/openvpn/client", certPath),
			filepath.Join("/etc/pia", certPath),
		)
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("CA certificate file not found: %s", certPath)
}

// notifyRefreshed performs a non-blocking send on the refreshed channel.
func notifyRefreshed(refreshed chan<- struct{}) {
	if refreshed == nil {
		return
	}
	select {
	case refreshed <- struct{}{}:
	default:
	}
}

// runPortForwardingLoop handles the port forwarding refresh loop. It blocks
// until the context is canceled or initialization fails permanently.
//
// Readiness is only signaled after the port has been bound and successfully
// published (written + script), so runService never reports success for a loop
// that is still retrying its initial binding.
func runPortForwardingLoop(ctx context.Context, cfg *config.Config, connect clientConnector, refreshed chan struct{}) {
	ticker := time.NewTicker(cfg.RefreshInterval)
	defer ticker.Stop()

	client, err := connectWithRetry(ctx, cfg.VPNRetryInterval, connect)
	if err != nil {
		log.Printf("Failed to establish port forwarding client: %v", err)
		return
	}

	pfInfo, err := getPortForwardingWithRetry(ctx, cfg.VPNRetryInterval, client)
	if err != nil {
		log.Printf("Failed to get initial port forwarding info: %v", err)
		return
	}

	log.Printf("Obtained port forwarding: port=%d, expires=%s", pfInfo.Port, pfInfo.ExpiresAt)

	initialPort := pfInfo.Port
	portChanged := true // initial execution should run the script

	pfInfo, client, initialPort, portChanged, ok := bindAndPublish(ctx, cfg, connect, pfInfo, client, initialPort, portChanged)
	if !ok {
		return
	}
	notifyRefreshed(refreshed)

	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}

		// Request a new signature if the current one is close to expiry.
		if time.Until(pfInfo.ExpiresAt) < 24*time.Hour {
			newInfo, changed := refreshPortForwarding(client, pfInfo, initialPort)
			if newInfo != pfInfo {
				if changed {
					portChanged = true
				}
				initialPort = newInfo.Port
				pfInfo = newInfo
			}
		}

		pfInfo, client, initialPort, portChanged, ok = bindAndPublish(ctx, cfg, connect, pfInfo, client, initialPort, portChanged)
		if !ok {
			return
		}
		notifyRefreshed(refreshed)
	}
}

// bindAndPublish binds the current payload/signature and publishes the port.
//
// On a bind failure it reconnects (re-detecting the VPN gateway/hostname),
// requests a fresh signature and retries with bounded exponential backoff. The
// change flag is preserved across failed binds and failed output writes so the
// port-change script is retried until it succeeds. It returns ok=false only
// when ctx is canceled.
func bindAndPublish(
	ctx context.Context,
	cfg *config.Config,
	connect clientConnector,
	pfInfo *portforwarding.PortForwardingInfo,
	client portForwardingClient,
	initialPort int,
	portChanged bool,
) (*portforwarding.PortForwardingInfo, portForwardingClient, int, bool, bool) {
	bo := newBackoff(cfg.VPNRetryInterval)

	for {
		if err := client.BindPort(pfInfo.Payload, pfInfo.Signature); err != nil {
			log.Printf("Failed to bind port: %v", err)
			if !waitForRetry(ctx, bo.next()) {
				return pfInfo, client, initialPort, portChanged, false
			}
			// The VPN tunnel may have reconnected with a new gateway; rebuild
			// the client and request a fresh signature before retrying.
			newClient, newInfo := reconnectAndRefresh(ctx, cfg, connect)
			if newClient != nil {
				client = newClient
			}
			if newInfo != nil {
				if newInfo.Port != initialPort {
					portChanged = true
				}
				initialPort = newInfo.Port
				pfInfo = newInfo
			}
			continue
		}

		log.Printf("Successfully bound port %d", pfInfo.Port)
		if err := handlePortOutput(pfInfo.Port, cfg, portChanged); err != nil {
			log.Printf("Failed to publish port %d: %v", pfInfo.Port, err)
			if !waitForRetry(ctx, bo.next()) {
				return pfInfo, client, initialPort, portChanged, false
			}
			// Preserve portChanged and retry bind + publish.
			continue
		}
		return pfInfo, client, initialPort, false, true
	}
}

// backoff produces bounded exponential delays starting at one second (or the
// configured cap if smaller) and doubling up to max. Recreated per burst so a
// recovered link resets the delay.
type backoff struct {
	current time.Duration
	max     time.Duration
}

func newBackoff(max time.Duration) *backoff {
	if max <= 0 {
		max = time.Minute
	}
	base := time.Second
	if max < base {
		base = max
	}
	return &backoff{current: base, max: max}
}

func (b *backoff) next() time.Duration {
	d := b.current
	if b.current < b.max {
		b.current *= 2
		if b.current > b.max {
			b.current = b.max
		}
	}
	return d
}

// connectWithRetry calls connect until it succeeds or the context is canceled.
func connectWithRetry(ctx context.Context, retryInterval time.Duration, connect clientConnector) (portForwardingClient, error) {
	if retryInterval <= 0 {
		retryInterval = time.Minute
	}

	var lastErr error
	for {
		client, err := connect(ctx)
		if err == nil {
			return client, nil
		}
		lastErr = err
		log.Printf("Failed to establish port forwarding client: %v. Retrying in %s...", err, retryInterval)

		if !waitForRetry(ctx, retryInterval) {
			return nil, fmt.Errorf("port forwarding connection canceled: %w", lastErr)
		}
	}
}

// getPortForwardingWithRetry requests port forwarding info until it succeeds or
// the context is canceled.
func getPortForwardingWithRetry(ctx context.Context, retryInterval time.Duration, client portForwardingClient) (*portforwarding.PortForwardingInfo, error) {
	if retryInterval <= 0 {
		retryInterval = time.Minute
	}

	var lastErr error
	for {
		pfInfo, err := client.GetPortForwarding()
		if err == nil {
			return pfInfo, nil
		}
		lastErr = err
		log.Printf("Failed to get port forwarding info: %v. Retrying in %s...", err, retryInterval)

		if !waitForRetry(ctx, retryInterval) {
			return nil, fmt.Errorf("port forwarding request canceled: %w", lastErr)
		}
	}
}

// reconnectAndRefresh rebuilds the port forwarding client and, if possible,
// obtains fresh port forwarding info.
func reconnectAndRefresh(ctx context.Context, cfg *config.Config, connect clientConnector) (portForwardingClient, *portforwarding.PortForwardingInfo) {
	client, err := connectWithRetry(ctx, cfg.VPNRetryInterval, connect)
	if err != nil {
		log.Printf("Failed to reconnect after bind failure: %v", err)
		return nil, nil
	}
	info, err := getPortForwardingWithRetry(ctx, cfg.VPNRetryInterval, client)
	if err != nil {
		log.Printf("Failed to refresh port forwarding after reconnect: %v", err)
		return client, nil
	}
	log.Printf("Reconnected port forwarding: port=%d, expires=%s", info.Port, info.ExpiresAt)
	return client, info
}

// refreshPortForwarding gets a new port forwarding signature when needed. It
// returns the (possibly unchanged on error) info and whether the port changed.
func refreshPortForwarding(client portForwardingClient, pfInfo *portforwarding.PortForwardingInfo, initialPort int) (*portforwarding.PortForwardingInfo, bool) {
	log.Printf("Port forwarding signature expiring soon, requesting a new one")
	newPfInfo, err := client.GetPortForwarding()
	if err != nil {
		log.Printf("Failed to get new port forwarding info: %v", err)
		return pfInfo, false
	}

	portChanged := newPfInfo.Port != initialPort
	log.Printf("Obtained new port forwarding: port=%d, expires=%s", newPfInfo.Port, newPfInfo.ExpiresAt)
	return newPfInfo, portChanged
}

// handlePortOutput writes the port to file and executes the script if needed.
// It returns an error if the output file could not be written.
func handlePortOutput(port int, cfg *config.Config, portChanged bool) error {
	// Write the port to the output file
	if err := portforwarding.WritePortToFile(port, cfg.OutputFile); err != nil {
		return fmt.Errorf("failed to write port to file: %w", err)
	}

	log.Printf("Wrote port %d to file: %s", port, cfg.OutputFile)

	// Execute port change script if configured, but only if the port has changed
	if cfg.OnPortChangeScript != "" && portChanged {
		log.Printf("Port changed, executing script")
		if err := executePortChangeScript(cfg, port); err != nil {
			return fmt.Errorf("port change script failed: %w", err)
		}
	}

	return nil
}

// runService runs the authenticated, VPN-connected port forwarding service
// until the context is canceled.
func runService(ctx context.Context, cfg *config.Config) error {
	username, password, err := cfg.LoadCredentials()
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	// A single auth client is reused for the lifetime of the service. Calling
	// GetToken (directly and via the port forwarding token provider) returns a
	// cached token until it nears expiry, then transparently refreshes it.
	authClient := auth.NewClient(username, password)
	if _, err := retryGetToken(ctx, cfg.VPNRetryInterval, authClient.GetToken); err != nil {
		return err
	}

	caCertPath, err := resolveCACertPath(cfg.CACertFile)
	if err != nil {
		return err
	}
	log.Printf("Using CA certificate: %s", caCertPath)

	connect := func(ctx context.Context) (portForwardingClient, error) {
		log.Printf("Detecting OpenVPN connection...")
		connInfo, err := detectVPNWithRetry(ctx, cfg.VPNRetryInterval, cfg.OpenVPNConfigFile, vpn.DetectOpenVPNConnection)
		if err != nil {
			return nil, fmt.Errorf("failed to detect OpenVPN connection: %w", err)
		}
		log.Printf("Detected OpenVPN connection: gateway=%s, hostname=%s", connInfo.GatewayIP, connInfo.Hostname)

		client, err := portforwarding.NewClient("", connInfo.GatewayIP, connInfo.Hostname, caCertPath)
		if err != nil {
			return nil, err
		}
		// Refresh the authentication token automatically for long-running
		// services instead of using the token captured at startup.
		client.SetTokenProvider(authClient.GetToken)
		return client, nil
	}

	// Buffered so the loop's non-blocking readiness notification cannot be
	// dropped if it fires before the select below starts waiting.
	refreshed := make(chan struct{}, 1)
	done := make(chan struct{})

	// The loop is given its own cancelable context so a startup timeout can
	// stop it and wait for a clean exit before returning.
	loopCtx, cancelLoop := context.WithCancel(ctx)
	defer cancelLoop()

	go func() {
		defer close(done)
		runPortForwardingLoop(loopCtx, cfg, connect, refreshed)
	}()

	// Allow enough time for several reconnect/backoff attempts when the VPN or
	// bind is not ready yet, without waiting forever before systemd restarts.
	startupTimeout := initializationTimeout
	if minStartup := 2 * cfg.VPNRetryInterval; minStartup > startupTimeout {
		startupTimeout = minStartup
	}

	if err := waitForInitialization(ctx, refreshed, done, cancelLoop, startupTimeout); err != nil {
		return err
	}

	// Initialized successfully: wait for a termination signal, then stop the
	// loop and wait for it to exit cleanly.
	<-ctx.Done()
	cancelLoop()
	<-done
	log.Printf("Received termination signal, shutting down...")
	return nil
}

// waitForInitialization waits for the loop to report readiness. On timeout or
// parent-context cancellation it cancels the loop and waits for it to exit, so
// no goroutine is leaked when runService returns.
func waitForInitialization(ctx context.Context, refreshed, done <-chan struct{}, cancel func(), startupTimeout time.Duration) error {
	select {
	case <-refreshed:
		log.Printf("Port forwarding initialized successfully")
		return nil
	case <-time.After(startupTimeout):
		cancel()
		<-done
		return fmt.Errorf("timed out after %s waiting for port forwarding initialization", startupTimeout)
	case <-ctx.Done():
		cancel()
		<-done
		return nil
	}
}

func main() {
	// Create a default configuration
	cfg := config.DefaultConfig()

	// Setup and parse command line flags
	if err := config.SetupFlags(cfg); err != nil {
		log.Fatalf("Invalid command line: %v", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Set up logging
	setupLogging(cfg.Debug)

	// Log configuration information
	logConfigInfo(cfg)

	// signal.NotifyContext cancels the context on SIGINT/SIGTERM without the
	// races of manually re-raising signals or sharing a channel between
	// goroutines.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runService(ctx, cfg); err != nil {
		log.Fatalf("%v", err)
	}
}

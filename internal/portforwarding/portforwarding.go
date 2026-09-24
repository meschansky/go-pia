package portforwarding

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// SignatureEndpoint is the endpoint for getting a port forwarding signature
	SignatureEndpoint = "getSignature"
	// BindPortEndpoint is the endpoint for binding a port
	BindPortEndpoint = "bindPort"
	// APIPort is the port for the PIA port forwarding API
	APIPort = "19999"
	// MaxPort is the highest valid TCP/UDP port number
	MaxPort = 65535
)

// TokenProvider returns a currently valid PIA authentication token. It is
// invoked before every API request so that a long-running service can obtain a
// fresh token after the previous one expires (PIA tokens are valid for 24h).
type TokenProvider func() (string, error)

// Client handles port forwarding operations
type Client struct {
	httpClient    *http.Client
	token         string
	tokenProvider TokenProvider
	gatewayIP     string
	hostname      string
	caCertPath    string
	apiPort       string
}

// PayloadAndSignature represents the response from the getSignature endpoint
type PayloadAndSignature struct {
	Status    string `json:"status"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// PayloadData represents the decoded payload data
type PayloadData struct {
	Port      int       `json:"port"`
	ExpiresAt time.Time `json:"expires_at"`
}

// BindPortResponse represents the response from the bindPort endpoint
type BindPortResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// PortForwardingInfo contains information about the forwarded port
type PortForwardingInfo struct {
	Port      int
	ExpiresAt time.Time
	Payload   string
	Signature string
}

// NewClient creates a new port forwarding client.
//
// The PIA API is reached at the port forwarding hostname (used for TLS server
// name verification and the HTTP Host header) but the TCP connection is made
// to the VPN gateway IP, mirroring curl's `--connect-to "$PF_HOSTNAME::$PF_GATEWAY:"`.
// The provided CA certificate is the PIA CA that signs the port forwarding
// server certificate; it is loaded into the TLS root pool and certificate
// verification is always performed (InsecureSkipVerify is never set).
func NewClient(token, gatewayIP, hostname, caCertPath string) (*Client, error) {
	if hostname == "" {
		return nil, fmt.Errorf("port forwarding hostname is required for TLS verification")
	}
	if gatewayIP == "" {
		return nil, fmt.Errorf("VPN gateway IP is required")
	}
	if caCertPath == "" {
		return nil, fmt.Errorf("CA certificate path is required")
	}

	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate %q: %w", caCertPath, err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate %q", caCertPath)
	}

	tlsConfig := &tls.Config{
		RootCAs:    roots,
		ServerName: hostname,
		MinVersion: tls.VersionTLS12,
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
		token:      token,
		gatewayIP:  gatewayIP,
		hostname:   hostname,
		caCertPath: caCertPath,
		apiPort:    APIPort,
	}, nil
}

// SetTokenProvider configures a callback used to obtain a fresh token before
// each API request. When set it takes precedence over the static token passed
// to NewClient, enabling token refresh for long-running services.
func (c *Client) SetTokenProvider(provider TokenProvider) {
	c.tokenProvider = provider
}

// currentToken returns the token to use for an API request.
func (c *Client) currentToken() (string, error) {
	if c.tokenProvider != nil {
		token, err := c.tokenProvider()
		if err != nil {
			return "", fmt.Errorf("failed to obtain authentication token: %w", err)
		}
		if token == "" {
			return "", fmt.Errorf("authentication token provider returned an empty token")
		}
		return token, nil
	}
	if c.token == "" {
		return "", fmt.Errorf("no authentication token available")
	}
	return c.token, nil
}

// GetPortForwarding obtains port forwarding information from the PIA API
func (c *Client) GetPortForwarding() (*PortForwardingInfo, error) {
	// Get the payload and signature
	payloadAndSig, err := c.getSignature()
	if err != nil {
		return nil, fmt.Errorf("failed to get signature: %w", err)
	}

	if payloadAndSig.Payload == "" || payloadAndSig.Signature == "" {
		return nil, fmt.Errorf("port forwarding response missing payload or signature")
	}

	// Decode the payload to get the port and expiration
	payloadData, err := decodePayload(payloadAndSig.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
	}

	if err := validatePayloadData(payloadData); err != nil {
		return nil, err
	}

	return &PortForwardingInfo{
		Port:      payloadData.Port,
		ExpiresAt: payloadData.ExpiresAt,
		Payload:   payloadAndSig.Payload,
		Signature: payloadAndSig.Signature,
	}, nil
}

// BindPort binds the port to the VPN connection
func (c *Client) BindPort(payload, signature string) error {
	if payload == "" || signature == "" {
		return fmt.Errorf("payload and signature are required to bind a port")
	}

	params := url.Values{}
	params.Add("payload", payload)
	params.Add("signature", signature)

	body, err := c.doAPIRequest(http.MethodGet, BindPortEndpoint, params)
	if err != nil {
		return err
	}

	// Parse the response
	var bindResp BindPortResponse
	if err := json.Unmarshal(body, &bindResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Check if the binding was successful
	if bindResp.Status != "OK" {
		return fmt.Errorf("failed to bind port: %s", bindResp.Message)
	}

	return nil
}

// getSignature gets a port forwarding signature from the PIA API
func (c *Client) getSignature() (*PayloadAndSignature, error) {
	token, err := c.currentToken()
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Add("token", token)

	body, err := c.doAPIRequest(http.MethodGet, SignatureEndpoint, params)
	if err != nil {
		return nil, err
	}

	// Parse the response
	var payloadAndSig PayloadAndSignature
	if err := json.Unmarshal(body, &payloadAndSig); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check if the request was successful
	if payloadAndSig.Status != "OK" {
		return nil, fmt.Errorf("failed to get signature: status=%s", payloadAndSig.Status)
	}

	return &payloadAndSig, nil
}

// doAPIRequest performs an authenticated request against the PIA port
// forwarding API. The URL is built from the PIA hostname (so TLS verification
// and the Host header use it) while the connection is dialed to the VPN
// gateway IP.
func (c *Client) doAPIRequest(method, endpoint string, params url.Values) ([]byte, error) {
	apiURL := fmt.Sprintf("https://%s/%s", net.JoinHostPort(c.hostname, c.apiPort), endpoint)

	req, err := http.NewRequest(method, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.URL.RawQuery = params.Encode()
	req.Host = c.hostname
	req.URL.Host = net.JoinHostPort(c.gatewayIP, c.apiPort)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected HTTP status %s from %s: %s", resp.Status, endpoint, string(body))
	}

	return body, nil
}

// decodePayload decodes the base64-encoded payload
func decodePayload(payload string) (*PayloadData, error) {
	// Decode the payload from base64
	decodedBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload from base64: %w", err)
	}

	// Parse the JSON payload
	var payloadData PayloadData
	if err := json.Unmarshal(decodedBytes, &payloadData); err != nil {
		return nil, fmt.Errorf("failed to parse payload JSON: %w", err)
	}

	return &payloadData, nil
}

// validatePayloadData checks that a decoded payload contains a usable port and
// expiration. This guards against malformed or empty API responses.
func validatePayloadData(payloadData *PayloadData) error {
	if payloadData.Port <= 0 || payloadData.Port > MaxPort {
		return fmt.Errorf("invalid port in payload: %d", payloadData.Port)
	}
	if payloadData.ExpiresAt.IsZero() {
		return fmt.Errorf("payload missing expires_at")
	}
	if !payloadData.ExpiresAt.After(time.Now()) {
		return fmt.Errorf("port forwarding payload expired at %s", payloadData.ExpiresAt)
	}
	return nil
}

// WritePortToFile atomically writes the port number to a file.
//
// The data is written to a temporary file in the destination directory, synced
// to disk and then renamed over the destination so that readers never observe a
// partially written file.
func WritePortToFile(port int, filePath string) error {
	if port <= 0 || port > MaxPort {
		return fmt.Errorf("invalid port: %d", port)
	}

	// Create the directory if it doesn't exist
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".port-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we fail before the rename.
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to set permissions on temporary file: %w", err)
	}

	if _, err := tmp.WriteString(strconv.Itoa(port)); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write port to temporary file: %w", err)
	}

	// Flush to stable storage before the rename so a crash cannot leave an
	// empty/partial destination file.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temporary file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	if err := os.Rename(tmpName, filePath); err != nil {
		return fmt.Errorf("failed to replace output file: %w", err)
	}
	tmpName = "" // ownership transferred to filePath

	return nil
}

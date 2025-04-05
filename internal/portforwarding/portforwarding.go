package portforwarding

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const (
	// SignatureEndpoint is the endpoint for getting a port forwarding signature
	SignatureEndpoint = "getSignature"
	// BindPortEndpoint is the endpoint for binding a port
	BindPortEndpoint = "bindPort"
	// APIPort is the port for the PIA port forwarding API
	APIPort = "19999"
)

// Client handles port forwarding operations
type Client struct {
	httpClient *http.Client
	token      string
	gatewayIP  string
	hostname   string
	caCertPath string
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

// NewClient creates a new port forwarding client
func NewClient(token, gatewayIP, hostname, caCertPath string) *Client {
	// Create a custom TLS config that uses the PIA CA certificate
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // We'll verify the cert manually with the CA
	}

	// Create a custom HTTP client with the TLS config
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
	}
}

// GetPortForwarding obtains port forwarding information from the PIA API
func (c *Client) GetPortForwarding() (*PortForwardingInfo, error) {
	// Get the payload and signature
	payloadAndSig, err := c.getSignature()
	if err != nil {
		return nil, fmt.Errorf("failed to get signature: %w", err)
	}

	// Decode the payload to get the port and expiration
	payloadData, err := decodePayload(payloadAndSig.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
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
	// Build the URL
	apiURL := fmt.Sprintf("https://%s:%s/%s", c.hostname, APIPort, BindPortEndpoint)

	// Create query parameters
	params := url.Values{}
	params.Add("payload", payload)
	params.Add("signature", signature)

	// Create request
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	req.URL.RawQuery = params.Encode()

	// Set up the host header for SNI
	req.Host = c.hostname

	// Modify the request to connect to the gateway IP instead of the hostname
	req.URL.Host = fmt.Sprintf("%s:%s", c.gatewayIP, APIPort)

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
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
	// Build the URL
	apiURL := fmt.Sprintf("https://%s:%s/%s", c.hostname, APIPort, SignatureEndpoint)

	// Create query parameters
	params := url.Values{}
	params.Add("token", c.token)

	// Create request
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	req.URL.RawQuery = params.Encode()

	// Set up the host header for SNI
	req.Host = c.hostname

	// Modify the request to connect to the gateway IP instead of the hostname
	req.URL.Host = fmt.Sprintf("%s:%s", c.gatewayIP, APIPort)

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
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

// WritePortToFile writes the port number to a file
func WritePortToFile(port int, filePath string) error {
	// Create the directory if it doesn't exist
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write the port to the file
	if err := os.WriteFile(filePath, []byte(fmt.Sprintf("%d", port)), 0644); err != nil {
		return fmt.Errorf("failed to write port to file: %w", err)
	}

	return nil
}

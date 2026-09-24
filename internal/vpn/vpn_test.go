package vpn

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// withNetInterfaces substitutes netInterfaces for the duration of a test.
func withNetInterfaces(t *testing.T, ifaces []net.Interface, err error) {
	t.Helper()
	orig := netInterfaces
	netInterfaces = func() ([]net.Interface, error) { return ifaces, err }
	t.Cleanup(func() { netInterfaces = orig })
}

func TestHasTunInterface(t *testing.T) {
	testCases := []struct {
		name       string
		interfaces []net.Interface
		err        error
		expected   bool
	}{
		{
			name: "Has tun interface",
			interfaces: []net.Interface{
				{Name: "eth0"},
				{Name: "tun0"},
				{Name: "lo"},
			},
			expected: true,
		},
		{
			name: "No tun interface",
			interfaces: []net.Interface{
				{Name: "eth0"},
				{Name: "lo"},
			},
			expected: false,
		},
		{
			name:       "Error getting interfaces",
			interfaces: nil,
			err:        os.ErrNotExist,
			expected:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			withNetInterfaces(t, tc.interfaces, tc.err)
			if got := hasTunInterface(); got != tc.expected {
				t.Errorf("hasTunInterface() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestParseVPNGateway(t *testing.T) {
	testCases := []struct {
		name      string
		output    string
		expected  string
		expectErr bool
	}{
		{
			name: "Default route via tun",
			output: `default via 10.0.0.1 dev eth0
default via 10.230.208.1 dev tun0 proto static
10.230.208.0/24 dev tun0 proto kernel scope link src 10.230.208.2`,
			expected: "10.230.208.1",
		},
		{
			name: "OpenVPN split default routes",
			output: `0.0.0.0/1 via 10.230.208.1 dev tun0
128.0.0.0/1 via 10.230.208.1 dev tun0
10.230.208.0/24 dev tun0 proto kernel scope link src 10.230.208.2`,
			expected: "10.230.208.1",
		},
		{
			name:      "Only link route has no gateway",
			output:    `10.230.208.0/24 dev tun0 proto kernel scope link src 10.230.208.2`,
			expectErr: true,
		},
		{
			name: "No tun interface",
			output: `default via 192.168.1.1 dev eth0
192.168.1.0/24 dev eth0 proto kernel scope link src 192.168.1.10`,
			expectErr: true,
		},
		{
			name:      "Empty output",
			output:    "",
			expectErr: true,
		},
		{
			name:      "Invalid via address",
			output:    `default via not-an-ip dev tun0`,
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVPNGateway(tc.output)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, got gateway %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Errorf("parseVPNGateway() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestGetVPNGatewayIP(t *testing.T) {
	orig := commandRunner
	t.Cleanup(func() { commandRunner = orig })

	commandRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "ip" {
			t.Fatalf("unexpected command %q", name)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("expected command context to have a deadline")
		}
		return []byte("default via 10.230.208.1 dev tun0\n"), nil
	}

	ip, err := getVPNGatewayIP(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.230.208.1" {
		t.Errorf("getVPNGatewayIP() = %q, want 10.230.208.1", ip)
	}

	commandRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("ip not found")
	}
	if _, err := getVPNGatewayIP(context.Background()); err == nil {
		t.Error("expected error when command fails")
	}
}

// TestGetVPNGatewayIPContextCanceled verifies command cancellation propagates.
func TestGetVPNGatewayIPContextCanceled(t *testing.T) {
	orig := commandRunner
	t.Cleanup(func() { commandRunner = orig })

	commandRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := getVPNGatewayIP(ctx); err == nil {
		t.Error("expected error when context is canceled")
	}
}

func TestParseVPNRemote(t *testing.T) {
	testCases := []struct {
		name      string
		config    string
		expected  string
		expectErr bool
	}{
		{
			name:     "Hostname remote",
			config:   "dev tun\nremote us-newyorkcity.privacy.network 1194 udp\n",
			expected: "us-newyorkcity.privacy.network",
		},
		{
			name:     "IP remote",
			config:   "remote 192.168.1.1 1194 udp\n",
			expected: "192.168.1.1",
		},
		{
			name:      "No remote",
			config:    "dev tun\ncipher AES-256-CBC\n",
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVPNRemote(tc.config)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Errorf("parseVPNRemote() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestGetVPNHostname(t *testing.T) {
	origLookup := serverHostnameLookup
	t.Cleanup(func() { serverHostnameLookup = origLookup })

	// Default: server lookup fails.
	serverHostnameLookup = func(ctx context.Context, ip string) (string, error) {
		return "", errors.New("lookup unavailable")
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "test.ovpn")

	testCases := []struct {
		name           string
		configContent  string
		expectedResult string
		expectError    bool
	}{
		{
			name:           "Hostname in config",
			configContent:  "remote test.hostname.com 1194 udp\n",
			expectedResult: "test.hostname.com",
			expectError:    false,
		},
		{
			name:           "IP in config with failed lookup errors",
			configContent:  "remote 192.168.1.1 1194 udp\n",
			expectedResult: "",
			expectError:    true,
		},
		{
			name:           "No remote in config",
			configContent:  "dev tun\ncipher AES-256-CBC\n",
			expectedResult: "",
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(configFile, []byte(tc.configContent), 0644); err != nil {
				t.Fatalf("Failed to write test config file: %v", err)
			}

			result, err := getVPNHostname(context.Background(), configFile)

			if tc.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if !tc.expectError && result != tc.expectedResult {
				t.Errorf("Expected %q, got %q", tc.expectedResult, result)
			}
		})
	}

	// When the server lookup succeeds its result is used.
	serverHostnameLookup = func(ctx context.Context, ip string) (string, error) {
		if ip != "158.173.25.8" {
			t.Errorf("lookup called with %q", ip)
		}
		return "newyork433", nil
	}
	if err := os.WriteFile(configFile, []byte("remote 158.173.25.8 1194 udp\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	result, err := getVPNHostname(context.Background(), configFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "newyork433" {
		t.Errorf("expected resolved server hostname, got %q", result)
	}

	// Empty result from the lookup is also an error.
	serverHostnameLookup = func(ctx context.Context, ip string) (string, error) {
		return "", nil
	}
	if _, err := getVPNHostname(context.Background(), configFile); err == nil {
		t.Error("expected error for empty lookup result")
	}

	// Missing config file must error.
	if _, err := getVPNHostname(context.Background(), filepath.Join(tmpDir, "missing.ovpn")); err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestLookupServerHostname(t *testing.T) {
	const body = `{"regions":[
		{"servers":{"ovpnudp":[{"ip":"158.173.25.8","cn":"newyork433"}],"wg":[{"ip":"37.19.198.79","cn":"newyork432"}]}},
		{"servers":{"ovpnudp":[{"ip":"1.2.3.4","cn":"california101"}]}}
	]}{"signature":"ignored"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	origURL, origClient := piaServerListURL, serverListClient
	t.Cleanup(func() { piaServerListURL = origURL; serverListClient = origClient })
	piaServerListURL = server.URL
	serverListClient = server.Client()

	host, err := lookupServerHostname(context.Background(), "158.173.25.8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "newyork433" {
		t.Errorf("lookupServerHostname() = %q", host)
	}

	if _, err := lookupServerHostname(context.Background(), "9.9.9.9"); err == nil {
		t.Error("expected error for unknown IP")
	}

	// HTTP error status should fail.
	piaServerListURL = server.URL + "/error"
	serverListClient = server.Client()
	if _, err := lookupServerHostname(context.Background(), "158.173.25.8"); err == nil {
		t.Error("expected error for non-200 response")
	}

	// Context cancellation must abort the request.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	piaServerListURL = server.URL
	serverListClient = server.Client()
	if _, err := lookupServerHostname(cancelCtx, "158.173.25.8"); err == nil {
		t.Error("expected error for canceled context")
	}
}

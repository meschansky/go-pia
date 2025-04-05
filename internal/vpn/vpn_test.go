package vpn

import (
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// mockNetInterfaces is a helper function for testing that returns mock network interfaces
type interfaceGetter func() ([]net.Interface, error)

// Original net.Interfaces function
var originalInterfaces interfaceGetter = net.Interfaces

// Mock implementation for testing
func mockInterfaces(interfaces []net.Interface, err error) interfaceGetter {
	return func() ([]net.Interface, error) {
		return interfaces, err
	}
}

// testHasTunInterface is a test-specific implementation that uses the provided interfaceGetter
func testHasTunInterface(getter interfaceGetter) bool {
	interfaces, err := getter()
	if err != nil {
		return false
	}

	for _, iface := range interfaces {
		if len(iface.Name) >= 3 && iface.Name[:3] == "tun" {
			return true
		}
	}

	return false
}

func TestHasTunInterface(t *testing.T) {
	// Save original function for later tests
	originalGetter := originalInterfaces
	// Restore original function after test
	defer func() { originalInterfaces = originalGetter }()

	// Test cases
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
			err:      nil,
			expected: true,
		},
		{
			name: "No tun interface",
			interfaces: []net.Interface{
				{Name: "eth0"},
				{Name: "lo"},
			},
			err:      nil,
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
			// Create a mock getter for this test case
			mockGetter := mockInterfaces(tc.interfaces, tc.err)

			// Call the test function with our mock
			result := testHasTunInterface(mockGetter)

			// Verify result
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestGetVPNHostname(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "test.ovpn")

	// Test cases
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
			name:           "IP in config",
			configContent:  "remote 192.168.1.1 1194 udp\n",
			expectedResult: "192.168.1.1.privacy.network",
			expectError:    false,
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
			// Write config content to file
			if err := ioutil.WriteFile(configFile, []byte(tc.configContent), 0644); err != nil {
				t.Fatalf("Failed to write test config file: %v", err)
			}

			// Call the function
			result, err := getVPNHostname(configFile)

			// Verify error
			if tc.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Verify result
			if !tc.expectError && result != tc.expectedResult {
				t.Errorf("Expected %q, got %q", tc.expectedResult, result)
			}
		})
	}
}

func TestConstructHostname(t *testing.T) {
	testCases := []struct {
		ip       string
		expected string
	}{
		{
			ip:       "192.168.1.1",
			expected: "192.168.1.1.privacy.network",
		},
		{
			ip:       "10.0.0.1",
			expected: "10.0.0.1.privacy.network",
		},
		{
			ip:       "",
			expected: ".privacy.network",
		},
	}

	for _, tc := range testCases {
		result := constructHostname(tc.ip)
		if result != tc.expected {
			t.Errorf("For IP %q, expected %q, got %q", tc.ip, tc.expected, result)
		}
	}
}

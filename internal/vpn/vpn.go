package vpn

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

// ConnectionInfo holds information about the VPN connection
type ConnectionInfo struct {
	GatewayIP string
	Hostname  string
}

// DetectOpenVPNConnection detects an active OpenVPN connection and returns connection info
func DetectOpenVPNConnection(ovpnConfigPath string) (*ConnectionInfo, error) {
	// Check if tun interface exists
	if !hasTunInterface() {
		return nil, fmt.Errorf("no active OpenVPN connection detected (no tun interface)")
	}

	// Get gateway IP from routing table
	gatewayIP, err := getVPNGatewayIP()
	if err != nil {
		return nil, fmt.Errorf("failed to get VPN gateway IP: %w", err)
	}

	// Get hostname from OpenVPN config
	hostname, err := getVPNHostname(ovpnConfigPath)
	if err != nil {
		// If we can't get the hostname from the config, try to construct it from the gateway IP
		hostname = constructHostname(gatewayIP)
	}

	return &ConnectionInfo{
		GatewayIP: gatewayIP,
		Hostname:  hostname,
	}, nil
}

// hasTunInterface checks if a tun interface exists
func hasTunInterface() bool {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false
	}

	for _, iface := range interfaces {
		if strings.HasPrefix(iface.Name, "tun") {
			return true
		}
	}

	return false
}

// getVPNGatewayIP gets the VPN gateway IP from the routing table
func getVPNGatewayIP() (string, error) {
	// Run "ip route" command and parse the output to find the gateway IP for the tun interface
	cmd := exec.Command("ip", "route")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get routing table: %w", err)
	}

	// Parse the output to find the gateway IP
	// Look for lines containing "tun" and extract the gateway IP
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "tun") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				return fields[2], nil // The gateway IP is typically the 3rd field
			}
		}
	}

	return "", fmt.Errorf("VPN gateway IP not found in routing table")
}

// getVPNHostname gets the VPN server hostname from the OpenVPN config
func getVPNHostname(configPath string) (string, error) {
	// Read the OpenVPN config file
	file, err := os.Open(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to open OpenVPN config: %w", err)
	}
	defer file.Close()

	// Parse the config to find the remote server entry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "remote ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				// Check if the second field is an IP or hostname
				if net.ParseIP(fields[1]) != nil {
					// It's an IP, so we need to determine the hostname
					return constructHostname(fields[1]), nil
				} else {
					// It's already a hostname
					return fields[1], nil
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading OpenVPN config: %w", err)
	}

	return "", fmt.Errorf("VPN server hostname not found in OpenVPN config")
}

// constructHostname constructs a PIA hostname from an IP address
func constructHostname(ip string) string {
	return ip + ".privacy.network"
}

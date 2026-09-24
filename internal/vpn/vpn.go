package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// piaServerListURL is PIA's public server list. It is used to map a VPN
// server IP (as found in an OpenVPN `remote` directive) to the server name
// (`cn`) that appears in the server's TLS certificate. It is a variable so
// tests can point it at a local server.
var piaServerListURL = "https://serverlist.piaservers.net/vpninfo/servers/v6"

// commandTimeout bounds a single local command invocation (e.g. `ip route`).
const commandTimeout = 5 * time.Second

// commandRunner runs a command bound to ctx and returns its output. It is a
// package variable so tests can substitute deterministic command output while
// exercising the real parsing functions.
var commandRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// serverHostnameLookup resolves a PIA server IP to the server hostname used for
// TLS verification. It is a package variable so tests can substitute a stub.
var serverHostnameLookup = lookupServerHostname

// serverListClient is used for the PIA server list lookup and can be replaced
// in tests.
var serverListClient = &http.Client{Timeout: 10 * time.Second}

// ConnectionInfo holds information about the VPN connection
type ConnectionInfo struct {
	GatewayIP string
	Hostname  string
}

// DetectOpenVPNConnection detects an active OpenVPN connection and returns
// connection info. Blocking operations honor ctx.
func DetectOpenVPNConnection(ctx context.Context, ovpnConfigPath string) (*ConnectionInfo, error) {
	// Check if tun interface exists
	if !hasTunInterface() {
		return nil, fmt.Errorf("no active OpenVPN connection detected (no tun interface)")
	}

	// Get gateway IP from routing table
	gatewayIP, err := getVPNGatewayIP(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get VPN gateway IP: %w", err)
	}

	// Get hostname from OpenVPN config
	hostname, err := getVPNHostname(ctx, ovpnConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to determine VPN hostname: %w", err)
	}
	if hostname == "" {
		return nil, fmt.Errorf("failed to determine VPN hostname: empty result")
	}

	return &ConnectionInfo{
		GatewayIP: gatewayIP,
		Hostname:  hostname,
	}, nil
}

// netInterfaces lists network interfaces. It is a package variable so tests
// can substitute deterministic results.
var netInterfaces = net.Interfaces

// hasTunInterface checks if a tun interface exists
func hasTunInterface() bool {
	interfaces, err := netInterfaces()
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
func getVPNGatewayIP(ctx context.Context) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	output, err := commandRunner(cmdCtx, "ip", "route")
	if err != nil {
		return "", fmt.Errorf("failed to get routing table: %w", err)
	}

	return parseVPNGateway(string(output))
}

// parseVPNGateway extracts the VPN gateway IP from `ip route` output.
//
// OpenVPN normally installs routes of the form
//
//	default via 10.230.208.1 dev tun0 ...
//	0.0.0.0/1 via 10.230.208.1 dev tun0 ...
//
// We look for a route that egresses a tun* device and carries a `via` address,
// preferring an explicit default route. Only valid IP addresses are accepted,
// so lines such as `10.230.208.0/24 dev tun0 proto kernel scope link src ...`
// (which have no via and whose third field is the device name) are ignored.
func parseVPNGateway(output string) (string, error) {
	var fallback string

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		var dev, via string
		for i := 0; i < len(fields)-1; i++ {
			switch fields[i] {
			case "dev":
				dev = fields[i+1]
			case "via":
				via = fields[i+1]
			}
		}

		if !strings.HasPrefix(dev, "tun") || net.ParseIP(via) == nil {
			continue
		}

		if fields[0] == "default" {
			return via, nil
		}
		if fallback == "" {
			fallback = via
		}
	}

	if fallback != "" {
		return fallback, nil
	}

	return "", fmt.Errorf("VPN gateway IP not found in routing table")
}

// getVPNHostname gets the VPN server hostname to use for TLS verification.
//
// The OpenVPN `remote` directive usually contains an IP address. PIA server
// certificates are issued for the individual server name (for example
// `newyork433`, exposed as the `cn` field in PIA's public server list), not for
// the raw IP or the region DNS name, so when the remote is an IP we resolve the
// matching server name from PIA's public server list. If that lookup fails we
// return an error rather than inventing a name that cannot match the
// certificate: silently proceeding would only fail later during the TLS
// handshake with a much less actionable message.
func getVPNHostname(ctx context.Context, configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to open OpenVPN config: %w", err)
	}

	remote, err := parseVPNRemote(string(data))
	if err != nil {
		return "", err
	}

	if net.ParseIP(remote) == nil {
		// Already a hostname.
		return remote, nil
	}

	hostname, err := serverHostnameLookup(ctx, remote)
	if err != nil {
		return "", fmt.Errorf("could not determine the PIA TLS server name for OpenVPN remote %s: %w; ensure PIA's server list is reachable, or configure the OpenVPN remote directive with the server hostname", remote, err)
	}
	if hostname == "" {
		return "", fmt.Errorf("could not determine the PIA TLS server name for OpenVPN remote %s: server list returned an empty name", remote)
	}
	return hostname, nil
}

// parseVPNRemote extracts the server address from the first `remote` directive
// in an OpenVPN configuration.
func parseVPNRemote(config string) (string, error) {
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "remote ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] != "" {
			return fields[1], nil
		}
	}

	return "", fmt.Errorf("VPN server hostname not found in OpenVPN config")
}

// piaServerList mirrors the subset of PIA's server list API that we need. Each
// server entry carries the IP used to reach the server and the `cn` name that
// appears in its TLS certificate.
type piaServerList struct {
	Regions []struct {
		DNS     string `json:"dns"`
		Servers map[string][]struct {
			IP string `json:"ip"`
			CN string `json:"cn"`
		} `json:"servers"`
	} `json:"regions"`
}

// lookupServerHostname fetches PIA's public server list and maps a server IP to
// the server name (`cn`) present in its TLS certificate. The request is bound
// to ctx.
func lookupServerHostname(ctx context.Context, ip string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, piaServerListURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build PIA server list request: %w", err)
	}

	resp, err := serverListClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch PIA server list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected HTTP status fetching PIA server list: %s", resp.Status)
	}

	var list piaServerList
	// The response contains a JSON document followed by a signature; Decode
	// reads the first JSON value and leaves the trailing data unread.
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", fmt.Errorf("failed to parse PIA server list: %w", err)
	}

	for _, region := range list.Regions {
		for _, servers := range region.Servers {
			for _, server := range servers {
				if server.IP == ip && server.CN != "" {
					return server.CN, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no PIA server found for IP %s", ip)
}

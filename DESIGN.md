# Go PIA Port Forwarding Service - Design Document

## Overview

This document outlines the design for a Go-based implementation of the PIA VPN port forwarding service for headless servers. The service will replicate the functionality provided by the PIA manual-connections bash scripts, specifically focusing on the port forwarding capabilities.

## System Requirements

- Go (1.24+)
- OpenVPN (already configured and running)
- PIA VPN subscription with port forwarding capability

## Architecture

The service will be structured as a standalone Go application with the following components:

### 1. Core Components

#### 1.1 Authentication Module
- Responsible for obtaining and refreshing PIA authentication tokens
- Reads credentials from a file (similar to the bash implementation)
- Handles token expiration and renewal

#### 1.2 VPN Connection Detection
- Detects the active OpenVPN connection
- Extracts gateway IP and hostname information
- Validates that a VPN connection is established before attempting port forwarding

#### 1.3 Port Forwarding Module
- Implements the PIA port forwarding protocol
- Obtains a port forwarding signature and payload
- Binds the port to the VPN connection
- Handles periodic refresh to maintain the port forwarding (every 15 minutes)

#### 1.4 Configuration Management
- Reads environment variables and configuration files
- Provides sensible defaults
- Validates configuration parameters

#### 1.5 Output Management
- Writes the forwarded port to a specified file
- Provides logging for monitoring and debugging

### 2. External Dependencies

- Standard Go libraries for HTTP requests, JSON parsing, and file I/O
- No external Go dependencies will be required for the core functionality

## Implementation Details

### Authentication Flow

1. Read PIA credentials from the specified file (format: username on first line, password on second line)
2. Make a POST request to the PIA authentication API to obtain a token
3. Store the token and its expiration time for subsequent requests
4. Implement token refresh logic to handle expiration

### Port Forwarding Flow

1. Detect the VPN gateway IP and hostname
2. Request a port forwarding signature using the authentication token
3. Extract the payload, signature, and port information
4. Bind the port by making a request to the VPN gateway
5. Set up a timer to refresh the port binding every 15 minutes
6. Write the port number to the specified output file

### Error Handling

- Comprehensive error handling for network failures, authentication issues, and VPN connection problems
- Automatic retry mechanisms with exponential backoff for transient failures
- Clear error messages and logging

### Security Considerations

- Secure handling of credentials and tokens
- TLS certificate validation for API requests
- No hardcoded secrets

## Command-Line Interface

The service will be invoked as follows:

```
PIA_CREDENTIALS="/path/to/credentials.txt" ./go-pia-port-forwarding /path/to/output/port.txt
```

Where:
- `PIA_CREDENTIALS` is an environment variable pointing to the file containing PIA credentials
- The first argument is the path to the file where the forwarded port will be written

Additional optional environment variables:
- `PIA_DEBUG`: Enable verbose logging (default: false)
- `PIA_REFRESH_INTERVAL`: Override the default 15-minute refresh interval (in seconds)

## Systemd Service

A sample systemd service file will be provided to run the port forwarding service as a daemon:

```
[Unit]
Description=PIA VPN Port Forwarding Service
After=network.target openvpn-client@pia.service

[Service]
Type=simple
Environment="PIA_CREDENTIALS=/etc/openvpn/server/pia.txt"
ExecStart=/usr/local/bin/go-pia-port-forwarding /var/run/pia-port.txt
Restart=on-failure
RestartSec=30

[Install]
WantedBy=multi-user.target
```

## Development Roadmap

### Phase 1: Core Implementation
- Implement authentication module
- Implement VPN connection detection
- Implement basic port forwarding functionality
- Add configuration management

### Phase 2: Reliability & Robustness
- Add comprehensive error handling
- Implement retry mechanisms
- Add logging and monitoring
- Implement graceful shutdown

### Phase 3: Packaging & Distribution
- Create systemd service file
- Add installation instructions
- Create release packages

## Testing Strategy

- Unit tests for individual components
- Integration tests for the complete flow
- Manual testing against the PIA VPN service

## Future Enhancements

- Support for WireGuard in addition to OpenVPN
- Web interface for monitoring port forwarding status
- Notification system for port changes or failures
- Support for multiple simultaneous VPN connections

## Determining PF_GATEWAY and PF_HOSTNAME

Based on the analysis of the `connect_to_openvpn_with_token.sh` script, the following approach will be used to determine the PF_GATEWAY and PF_HOSTNAME required for port forwarding:

### PF_GATEWAY Determination

For OpenVPN connections, the PF_GATEWAY is determined as follows:

1. When OpenVPN connects, it creates a route to the VPN server's internal network
2. The OpenVPN up script (in the reference implementation) writes the gateway IP to a file at `/opt/piavpn-manual/route_info`
3. This gateway IP is then used as the PF_GATEWAY value

In our Go implementation, we will:
1. Check if an OpenVPN connection is active by examining network interfaces (looking for tun* interfaces)
2. Extract the gateway IP from the routing table using the `ip route` command or directly accessing the network configuration
3. Store this gateway IP for use in port forwarding requests

Example code to get the gateway IP:
```go
// Get the VPN gateway IP from the routing table
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
```

### PF_HOSTNAME Determination

For the PF_HOSTNAME, the reference implementation uses the hostname of the VPN server that was used to establish the connection:

1. In the bash implementation, this is passed as an environment variable (OVPN_HOSTNAME) when starting the OpenVPN connection
2. This hostname is then reused as PF_HOSTNAME for port forwarding

In our Go implementation, we will:
1. Extract the hostname from the OpenVPN configuration file (typically at `/etc/openvpn/client/pia.ovpn`)
2. If the hostname cannot be determined from the config file, we'll fall back to using the server IP with a standard PIA hostname suffix

Example code to get the hostname:
```go
// Get the VPN server hostname from the OpenVPN config
func getVPNHostname(configPath string) (string, error) {
    // Read the OpenVPN config file
    content, err := ioutil.ReadFile(configPath)
    if err != nil {
        return "", fmt.Errorf("failed to read OpenVPN config: %w", err)
    }
    
    // Parse the config to find the remote server entry
    scanner := bufio.NewScanner(strings.NewReader(string(content)))
    for scanner.Scan() {
        line := scanner.Text()
        if strings.HasPrefix(line, "remote ") {
            fields := strings.Fields(line)
            if len(fields) >= 2 {
                // Check if the second field is an IP or hostname
                if net.ParseIP(fields[1]) != nil {
                    // It's an IP, so we need to determine the hostname
                    // For PIA, we can use a standard suffix
                    return fields[1] + ".privacy.network", nil
                } else {
                    // It's already a hostname
                    return fields[1], nil
                }
            }
        }
    }
    
    return "", fmt.Errorf("VPN server hostname not found in OpenVPN config")
}
```

By implementing these methods, our Go service will be able to automatically determine the PF_GATEWAY and PF_HOSTNAME values needed for port forwarding, without requiring manual input from the user.

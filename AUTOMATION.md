# Port Forwarding Automation

This document describes how to configure the go-pia-port-forwarding service to automatically run scripts when the forwarded port changes.

## Overview

When the PIA port forwarding service obtains a new port (either at startup or after token expiration), it can automatically execute a user-defined script with the new port number as an argument. This allows for automated configuration updates in applications that depend on the forwarded port.

## Configuration

### Command Line Option

The automation script can be specified using the `--on-port-change` command line option:

```bash
go-pia-port-forwarding --on-port-change=/path/to/your/script.sh /path/to/port/file.txt
```

### Environment Variable

Alternatively, you can use the `PIA_ON_PORT_CHANGE` environment variable:

```bash
PIA_ON_PORT_CHANGE=/path/to/your/script.sh go-pia-port-forwarding /path/to/port/file.txt
```

### Systemd Service Configuration

To use this feature with the systemd service, add the environment variable to your service file:

```ini
[Unit]
Description=PIA VPN Port Forwarding Service
After=network.target openvpn-client@pia.service

[Service]
Type=simple
Environment="PIA_CREDENTIALS=/etc/openvpn/server/pia.txt"
Environment="PIA_ON_PORT_CHANGE=/path/to/your/script.sh"
ExecStart=/usr/local/bin/go-pia-port-forwarding /var/run/pia-port.txt
Restart=on-failure
RestartSec=30

[Install]
WantedBy=multi-user.target
```

## Script Requirements

The specified script will be executed with the following arguments:

1. The new port number
2. The path to the port file

For example:

```bash
/path/to/your/script.sh 12345 /var/run/pia-port.txt
```

### Example Script

Here's an example script that updates a Transmission BitTorrent client configuration:

```bash
#!/bin/bash
# update_transmission.sh

PORT=$1
CONFIG_FILE="/etc/transmission/settings.json"

# Backup the config file
cp "$CONFIG_FILE" "$CONFIG_FILE.bak"

# Update the port in the config file
sed -i "s/\"peer-port\": [0-9]*/\"peer-port\": $PORT/" "$CONFIG_FILE"

# Restart the Transmission service
systemctl restart transmission-daemon

echo "Updated Transmission peer port to $PORT"
```

## Security Considerations

- The script will be executed with the same permissions as the go-pia-port-forwarding process
- Make sure the script has appropriate permissions (executable bit set)
- Consider the security implications of automated configuration changes
- Validate input in your script to prevent command injection

## Logging

When a port change occurs and the script is executed, the event will be logged. If the script execution fails, an error message will be logged with the exit code.

## Troubleshooting

If your script isn't being executed:

1. Check that the script path is correct and the file exists
2. Ensure the script has executable permissions (`chmod +x /path/to/your/script.sh`)
3. Verify the service has permission to execute the script
4. Check the system logs for any error messages

## Advanced Usage

### Script Timeout

By default, the script execution will timeout after 30 seconds. You can change this with the `PIA_SCRIPT_TIMEOUT` environment variable (value in seconds):

```bash
PIA_SCRIPT_TIMEOUT=60 PIA_ON_PORT_CHANGE=/path/to/your/script.sh go-pia-port-forwarding /path/to/port/file.txt
```

### Synchronous vs Asynchronous Execution

By default, the port forwarding service will execute scripts asynchronously (in the background), allowing the main service to continue running without interruption. This prevents automation scripts from blocking the port refreshing functionality.

If you prefer synchronous execution (waiting for the script to complete before continuing), you can set the `PIA_SYNC_SCRIPT` environment variable:

```bash
PIA_SYNC_SCRIPT=true PIA_ON_PORT_CHANGE=/path/to/your/script.sh go-pia-port-forwarding /path/to/port/file.txt
```

With synchronous execution, the service will wait for the script to complete before continuing. This is generally not recommended unless your script is very quick to execute.

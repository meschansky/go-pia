#!/bin/bash
# Example script to update Transmission BitTorrent client with new port
# This script is called by go-pia-port-forwarding when the port changes

# Check if we have the right number of arguments
if [ $# -lt 1 ]; then
    echo "Usage: $0 <port_number> [port_file_path]"
    exit 1
fi

# Get the port from the first argument
PORT=$1
PORT_FILE=${2:-"/var/run/pia-port.txt"}

echo "Updating Transmission with new port: $PORT"

# Transmission settings
TRANSMISSION_SETTINGS="$HOME/.config/transmission/settings.json"
TRANSMISSION_USER="your-username"
TRANSMISSION_PASS="your-password"

# Update Transmission port via RPC
# Uncomment and modify the following line to use transmission-remote
# transmission-remote -n "$TRANSMISSION_USER:$TRANSMISSION_PASS" -p "$PORT"

# Alternative: Update settings.json directly
# Make sure Transmission is not running when using this method
if [ -f "$TRANSMISSION_SETTINGS" ]; then
    # Create a backup
    cp "$TRANSMISSION_SETTINGS" "$TRANSMISSION_SETTINGS.bak"
    
    # Update the port in settings.json
    # This is a simple sed replacement - in production you might want to use jq
    sed -i "s/\"peer-port\": [0-9]*/\"peer-port\": $PORT/" "$TRANSMISSION_SETTINGS"
    
    echo "Updated Transmission settings file: $TRANSMISSION_SETTINGS"
    
    # Restart Transmission (uncomment the appropriate line for your system)
    # systemctl --user restart transmission.service
    # killall -HUP transmission-daemon
else
    echo "Transmission settings file not found: $TRANSMISSION_SETTINGS"
    exit 1
fi

echo "Port update completed successfully"
exit 0

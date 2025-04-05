# Go PIA Port Forwarding

![GitHub Workflow Status](https://img.shields.io/github/actions/workflow/status/meschansky/go-pia/build.yml?branch=main)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/meschansky/go-pia)](https://goreportcard.com/report/github.com/meschansky/go-pia)

A robust Go implementation of a port-forwarding service for Private Internet Access (PIA) VPN, designed for headless servers. This project provides a more reliable and maintainable alternative to the bash scripts in PIA's official manual-connections repository.

## 🚀 Features

- Automatically detects active OpenVPN connections
- Obtains and refreshes PIA authentication tokens
- Requests and maintains port forwarding
- Writes the forwarded port to a file for use by other applications
- Runs custom scripts when port changes (automation)
- Designed to run as a systemd service

## 📋 Prerequisites

- Go 1.24+
- OpenVPN (already configured and running)
- PIA VPN subscription with port forwarding capability
- PIA's CA certificate (`ca.rsa.4096.crt`) - included in this repository

## 🔧 Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/meschansky/go-pia.git
cd go-pia

# Build the application
make build
```

### Binary Releases

Download the latest release from the [Releases page](https://github.com/meschansky/go-pia/releases).

## ⚙️ Setup

1. **Configure OpenVPN for PIA**:
   - Store your PIA credentials in a file (e.g., `/etc/openvpn/client/pia.txt`) with the format:
     ```
     username
     password
     ```
   - Configure your OpenVPN .ovpn file with `auth-user-pass /etc/openvpn/client/pia.txt`
   - Make sure to use a PIA server that supports port forwarding

2. **Place the CA Certificate**:
   - The `ca.rsa.4096.crt` file must be accessible to the application
   - By default, it should be in the same directory as the executable
   - You can also specify a custom path using the `PIA_CA_CERT` environment variable

3. **Start OpenVPN**:
   ```bash
   sudo systemctl enable --now openvpn-client@pia
   ```

## 🖥️ Usage

### Basic Usage

```bash
PIA_CREDENTIALS="/etc/openvpn/client/pia.txt" ./bin/go-pia-port-forwarding /var/run/pia-port.txt
```

Where:
- `PIA_CREDENTIALS` points to your PIA credentials file
- The first argument is the path where the forwarded port will be written

### Environment Variables

| Variable | Description | Default |
|----------|-------------|--------|
| `PIA_CREDENTIALS` | Path to PIA credentials file | (Required) |
| `PIA_DEBUG` | Enable verbose logging | `false` |
| `PIA_REFRESH_INTERVAL` | Port forwarding refresh interval | `15m` |
| `PIA_ON_PORT_CHANGE` | Script to execute when port changes | (None) |
| `PIA_SCRIPT_TIMEOUT` | Timeout for script execution | `30s` |
| `PIA_SYNC_SCRIPT` | Run script synchronously | `false` |
| `PIA_CA_CERT` | Path to PIA CA certificate | `./ca.rsa.4096.crt` |

### Command Line Options

```
Usage: go-pia-port-forwarding [OPTIONS] OUTPUT_FILE

Options:
  --on-port-change=PATH  Script to execute when port changes
```

## 🔄 Port Change Automation

You can configure the service to run a script whenever the port changes:

```bash
PIA_ON_PORT_CHANGE="/path/to/your/script.sh" ./bin/go-pia-port-forwarding /var/run/pia-port.txt
```

The script will be called with two arguments:
1. The new port number
2. The path to the port file

Example script execution:
```bash
/path/to/your/script.sh 12345 /var/run/pia-port.txt
```

By default, scripts run asynchronously (in the background). For more details and advanced options, see [AUTOMATION.md](AUTOMATION.md).

## 🛠️ Running as a Systemd Service

1. **Copy the binary**:
   ```bash
   sudo cp ./bin/go-pia-port-forwarding /usr/local/bin/
   sudo cp ./ca.rsa.4096.crt /usr/local/bin/
   ```

2. **Copy and edit the systemd service file**:
   ```bash
   sudo cp go-pia-port-forwarding.service /etc/systemd/system/
   sudo nano /etc/systemd/system/go-pia-port-forwarding.service
   ```

3. **Enable and start the service**:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now go-pia-port-forwarding
   ```

4. **Check the status**:
   ```bash
   sudo systemctl status go-pia-port-forwarding
   journalctl -u go-pia-port-forwarding -f
   ```

## 📝 Examples

Check the [examples](./examples) directory for sample scripts:

- [update-transmission.sh](./examples/update-transmission.sh) - Updates Transmission BitTorrent client with the new port

## 🔍 How It Works

1. The service reads PIA credentials from the specified file
2. It obtains a PIA authentication token
3. It detects the active OpenVPN connection and extracts the gateway IP and hostname
4. It requests a port forwarding signature from the PIA API using the CA certificate
5. It binds the port to the VPN connection
6. It writes the port number to the specified output file
7. If configured, it executes a script with the port number as an argument
8. It refreshes the port binding every 15 minutes to keep it active

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- [Private Internet Access](https://www.privateinternetaccess.com/) for their VPN service and manual-connections scripts
- The Go community for the excellent libraries and tools

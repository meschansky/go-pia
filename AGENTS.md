# Project guidance

## Overview

Go PIA Port Forwarding is a headless Go service that obtains a forwarded port from Private Internet Access over an OpenVPN connection, maintains the binding, writes the port to a file, and optionally runs a script when the port changes.

## Development commands

- Go 1.27.1 or newer is required (`go.mod`). If Go is not on `PATH` in an automated session, use `/opt/homebrew/bin/mise exec go@1.27.1 -- go ...` where available.
- `make build` builds `./bin/go-pia-port-forwarding` for the current platform.
- `make build-aarch64` builds `./bin/go-pia-port-forwarding-arm64` for Linux ARM64; `make build-all` runs both builds.
- `make clean` removes build artifacts; `make install` installs the binary to the Go bin directory.
- `make test` runs `go test -v ./...`. For quicker or stronger checks, run `go test ./...`, `go test -race ./...`, and `go vet ./...`.
- `make run ARGS="/tmp/pia-port.txt"` runs the application locally; set `PIA_CREDENTIALS=/path/to/credentials.txt` and optionally `PIA_DEBUG=true` for a real PIA connection.

## Architecture and behavior

- `cmd/go-pia-port-forwarding/main.go`: configuration and signal handling, startup/retry and port-binding loop, port publication, synchronous/asynchronous port-change scripts. SIGINT/SIGTERM cancel the service context; retries and VPN reconnection must honor cancellation. Signal readiness only after a successful bind and port publication.
- `internal/auth/`: PIA token requests and cached token renewal (24-hour validity, refreshed shortly before expiry). The service reuses one auth client and supplies fresh tokens when requesting signatures.
- `internal/config/`: environment variables, flags, credentials-file parsing, and validation. Durations use Go duration strings (such as `15m`, `30s`, `60s`) and must be positive.
- `internal/portforwarding/`: PIA signature and bind requests over HTTPS, payload validation, CA-based TLS verification, and atomic port-file writes. Keep verification enabled and verify the TLS server name against the PIA certificate; connect to the VPN gateway IP. The default binding refresh interval is 15 minutes.
- `internal/vpn/`: detect a `tun` interface and parse `ip route` for the VPN gateway. For an OpenVPN `remote` specified as an IP, resolve the certificate server name from PIA's server list. Gateway and hostname must be detected again when rebinding fails after a tunnel change.

## Configuration

- Required: `PIA_CREDENTIALS` points to a file with username on the first line and password on the second; a positional `OUTPUT_FILE` argument specifies where to publish the port.
- Optional: `PIA_DEBUG`, `PIA_REFRESH_INTERVAL` (default `15m`), `PIA_ON_PORT_CHANGE`, `PIA_SCRIPT_TIMEOUT` (default `30s`), `PIA_SYNC_SCRIPT` (default `false`), `PIA_CA_CERT` (default `ca.rsa.4096.crt`), and `PIA_VPN_RETRY_INTERVAL` (default `60s`).
- CLI flags: `--credentials`, `--ca-cert`, `--openvpn-config`, `--on-port-change`, `--refresh-interval`, `--script-timeout`, `--vpn-retry-interval`, `--sync-script`, and `--debug`. Consult `README.md` for usage and installation.
- Scripts receive the new port and output-file path as arguments. Async is the default; ensure the timeout context stays alive until the child is reaped.

## Testing and deployment

- Package tests are in `*_test.go`; service-loop and script tests are in `cmd/go-pia-port-forwarding/main_test.go`. Test actual production paths with local HTTP/TLS servers or injected dependencies rather than duplicating production logic. Cover cancellation, reconnection, expiry, and file-write failures when changing long-running behavior.
- `go-pia-port-forwarding.service` depends on `openvpn-client@pia.service`, loads root-readable credentials through systemd `LoadCredential` (requires systemd 247+), runs as `nobody`, and publishes `/run/pia/port.txt` through `RuntimeDirectory=pia`. Its CA path is `/usr/local/etc/pia/ca.rsa.4096.crt`.
- Do not put credentials or tokens in source or logs. Preserve certificate verification, context-bounded network operations, and atomic port publication.

# PIA CA Certificate Requirement

## Overview

This document explains the purpose and importance of the `ca.rsa.4096.crt` certificate file included in this repository.

## What is the CA Certificate?

The `ca.rsa.4096.crt` file is Private Internet Access's (PIA) Certificate Authority (CA) certificate. It's used to establish secure connections to PIA's API endpoints for port forwarding functionality.

## Why is it Required?

1. **API Authentication**: When the application communicates with PIA's port forwarding API, it needs to verify that it's connecting to a legitimate PIA server.

2. **TLS Verification**: The application uses TLS (Transport Layer Security) to encrypt communications with PIA's API. The CA certificate is used to validate the server's identity.

3. **Security**: Without proper certificate validation, the application would be vulnerable to man-in-the-middle attacks.

## How is it Used?

In the port forwarding process, the application makes HTTPS requests to PIA's API endpoints. These requests include:

```
curl -s -m 5 \
--connect-to "$PF_HOSTNAME::$PF_GATEWAY:" \
--cacert "ca.rsa.4096.crt" \
-G --data-urlencode "token=${PIA_TOKEN}" \
"https://${PF_HOSTNAME}:19999/getSignature"
```

The `--cacert "ca.rsa.4096.crt"` parameter specifies the CA certificate to use for server validation.

## Certificate Source

The `ca.rsa.4096.crt` file is provided by PIA and is publicly available in their [manual-connections](https://github.com/pia-foss/manual-connections) repository. This certificate is used across all PIA client applications.

## Certificate Location

By default, the application expects the certificate file to be in the same directory as the executable. You can specify a custom path using the `PIA_CA_CERT` environment variable:

```bash
PIA_CA_CERT="/path/to/ca.rsa.4096.crt" ./bin/go-pia-port-forwarding /var/run/pia-port.txt
```

When running as a systemd service, the certificate path should be specified in the service file:

```ini
Environment="PIA_CA_CERT=/usr/local/bin/ca.rsa.4096.crt"
```

## Certificate Validity

The certificate is valid until April 12, 2034. If PIA issues a new certificate before that date, you'll need to update the file in your installation.

## Security Considerations

1. **Do not modify the certificate**: Any modifications to the certificate will break the application's ability to connect to PIA's API.

2. **Keep the certificate accessible**: The application needs read access to the certificate file.

3. **Certificate updates**: If PIA updates their CA certificate, you'll need to update your local copy accordingly.

## Troubleshooting

If you encounter errors related to certificate validation, check:

1. The certificate file exists at the expected location
2. The file has not been modified or corrupted
3. The application has permission to read the file
4. You're using the latest version of the certificate from PIA

Common error messages related to certificate issues:

- "x509: certificate signed by unknown authority"
- "Failed to establish TLS connection"
- "Certificate verification failed"

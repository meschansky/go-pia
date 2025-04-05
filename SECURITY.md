# Security Policy

## Supported Versions

Only the latest version of go-pia is currently being supported with security updates.

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |

## Reporting a Vulnerability

We take the security of go-pia seriously. If you believe you've found a security vulnerability, please follow these steps:

1. **Do not disclose the vulnerability publicly**
2. **Email the maintainers directly** at [your-email@example.com](mailto:your-email@example.com)
3. **Include details** about the vulnerability:
   - The version of go-pia you're using
   - Steps to reproduce the issue
   - Potential impact of the vulnerability
   - Any potential solutions you may have

## What to Expect

- We will acknowledge receipt of your vulnerability report within 3 business days
- We will provide a more detailed response within 7 days, indicating next steps
- We will work with you to understand and resolve the issue
- Once the vulnerability is fixed, we will publicly acknowledge your responsible disclosure (unless you prefer to remain anonymous)

## Security Best Practices for Users

When using go-pia, consider the following security best practices:

1. **Keep your PIA credentials secure**:
   - Store credentials in a file with appropriate permissions (e.g., `chmod 600`)
   - Consider using environment variables instead of files for sensitive data

2. **Script Execution Security**:
   - When using the port change automation feature, ensure your scripts validate inputs
   - Set appropriate permissions on your scripts
   - Be cautious about what actions your scripts perform

3. **Regular Updates**:
   - Keep go-pia updated to the latest version
   - Monitor the repository for security updates

Thank you for helping keep go-pia and its users safe!

# Security Policy

## Supported Versions

| Version | Supported |
|---|---|
| `v0.1.x-alpha` | :white_check_mark: Security fixes only |
| `< 0.1.0` | :x: |

## Reporting a Vulnerability

If you discover a potential security vulnerability in `accdb` (such as a memory safety violation, uncontrolled resource consumption, or parsing panic from untrusted input), please **do not open a public GitHub issue**.

Instead, please report it privately:
1. Open a **GitHub Security Advisory** under the repository's "Security" tab, or
2. Contact the maintainers privately via email: `security@accdb.dev` (or the repository owner's GitHub profile).

Please include:
- A description of the vulnerability.
- Steps to reproduce or a minimal proof-of-concept.
- Any impact assessment you have.

### Important: Do Not Share Sensitive Data
- **Never** attach real database files containing sensitive, proprietary, or Personally Identifiable Information (PII) to an issue or report.
- Please construct a sanitized database file or provide hex dumps with dummy data.

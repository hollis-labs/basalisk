# Security policy

## Supported versions

Basalisk is pre-release software with no tagged versions. Security fixes are made on `main`. There are no backports.

## Report a vulnerability

Do not include an exploit, token, credential, database or other sensitive material in a public issue.

Use GitHub's private vulnerability-reporting flow when the repository's Security tab offers it. If it is unavailable, contact a repository maintainer privately through a contact channel published on the Hollis Labs organization or maintainer profile. Include:

- the affected commit and operating system
- the surface involved (web GUI, `/api`, companion API, assurance endpoints)
- whether it was reached directly on loopback or through a proxy
- reproduction steps and the security impact
- a safe way to contact you about coordination

Maintainers will acknowledge a private report, investigate it, and coordinate disclosure; response times are best effort.

## Deployment boundary

Basalisk is a single-owner application for a private network.

- **Loopback only.** The server refuses a non-loopback `LISTEN_ADDR` and rejects non-loopback `Host` headers. It is not a TLS server.
- **Behind a proxy.** `BASALISK_HTTPS_PROXY=1` additionally accepts exactly the canonical host from a loopback proxy that sends a single `X-Forwarded-Proto: https`. The proxy must preserve `Host` and `Origin` and overwrite the protocol header; other forwarded headers are stripped.
- **Sessions.** Cookies are `HttpOnly` and `SameSite=Strict`; mutations require a custom request header and reject foreign origins; login, setup and password attempts are throttled in process.
- **Companion devices.** Paired devices hold credentials limited to context reads and observation writes.

## Data at rest

The owner profile, password hash, hashed session tokens, integration metadata and activity records are stored in a SQLite database in the data directory (`0700` directory, `0600` files). Credentials are kept by the shared secrets resolver in permission-limited **plaintext files** in the same directory, not encrypted at rest. Anyone who can read the data directory can read them. API responses never return stored keys, secret references or hashes, and there is no credential-reveal endpoint. Protect backups of the whole data directory as credentials.

## Current security limitations

- no password recovery or account reset workflow
- secrets are stored in plaintext files rather than an OS keychain
- the server is not a TLS endpoint and relies on a trusted local proxy
- pre-release contracts

These are deployment constraints, not hidden roadmap promises. Operate within them.

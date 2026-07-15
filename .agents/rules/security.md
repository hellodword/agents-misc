# Security and Privacy

## Trust boundaries

- Treat request bodies, headers, URLs, filenames, archives, uploads, environment values, config files, database content, and external service responses as untrusted at their boundary.
- Validate type, length, allowed values, and structure before use. Reject invalid input with a stable, non-sensitive error.
- Enforce identity, roles, permissions, ownership, prices, and authorization server-side. Never trust client assertions for them.
- Use parameterized SQL and structured subprocess arguments. Never interpolate untrusted data into SQL or shell source.
- Prevent path traversal by validating contract-defined names, resolving against an owned base, and verifying containment before access.
- Validate uploads by size and required content/format signals; store them outside executable/static roots with server-owned names.

## Secrets and least privilege

- Never commit secrets, credentials, private keys, `.env` contents, access tokens, real-user exports, or local credential stores.
- Keep error responses and logs free of tokens, passwords, cookies, private paths, SQL text with sensitive values, and raw personal data.
- Use minimum filesystem, database, network, action, and cloud permissions required by the task.
- Do not add secret use, cloud authentication, publishing, or deployment as an incidental change.

## HTTP and browser behavior

- Set explicit server and client timeouts and a request-body limit appropriate to the contract.
- Use secure, HttpOnly, SameSite cookies when cookie-based authentication exists; choose SameSite and Secure behavior for the actual deployment contract.
- Protect state-changing cookie-authenticated requests against CSRF. Do not add CSRF machinery to token-only non-browser flows without a relevant threat.
- Return bounded errors without stack traces or internal implementation details.

## Data and telemetry

- Never use real user data as a development fixture. Prefer small synthetic data.
- Treat an unknown database, backup, log, or export as real data until explicit evidence proves it disposable.
- Make telemetry opt-in unless an existing product contract says otherwise. Document collected fields, destination, retention, and disable behavior.
- A destructive operation requires exact user authorization and a verified recovery path; a backup alone is not authorization.

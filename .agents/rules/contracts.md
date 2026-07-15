# Contracts and Compatibility

## Classify before changing

Classify an API, config, CLI, database schema, persisted format, generated interface, protocol, or documented behavior as:

- **internal**: unexported, undocumented, unpersisted, and without a durable consumer;
- **durable/local**: persisted, documented, generated, or consumed by local users or automation;
- **public/external**: consumed outside the repository or published interface.

Treat an unknown classification as durable/local. Preserve durable and public behavior unless the user explicitly authorizes the exact breaking contract and operation. Apply that authorization only to its stated scope, then update documentation, examples, tests, and migration or recovery guidance.

## HTTP and structured data

- For a greenfield JSON HTTP API, maintain a Markdown contract covering methods, paths, authentication, request/response bodies, errors, pagination, and compatibility.
- Use this error envelope:

```json
{ "error": { "code": "stable_code", "message": "Safe message", "details": {} } }
```

- Include `details` only when the contract defines it; do not leak internal state.
- Reject unknown config fields by default. Do not invent environment-variable overrides without an explicit contract.
- Validate external data at the boundary and preserve unknown fields only when forward-compatible passthrough is deliberate and tested.

## Time, dates, locale, and Unicode

- Represent instants as UTC RFC 3339 with millisecond precision, for example `2026-07-15T12:34:56.789Z`.
- Model calendar dates separately from instants; do not apply a timezone to a date-only value.
- Store machine-readable values independently of localized presentation.
- Normalize Unicode, case, whitespace, filenames, or paths only when the contract defines the normalization and collision behavior.

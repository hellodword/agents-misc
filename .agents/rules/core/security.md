---
id: core.security
kind: core
triggers:
  - 'security'
  - 'secret'
  - 'auth'
  - 'token'
  - 'permission'
  - 'untrusted input'
summary: Avoid unsafe secret handling, injection risks, and insecure defaults.
load_with: []
---

# Security Rules

- Never commit real secrets, tokens, private keys, local credentials, production config, user uploads, or private data.
- Use `.env.example` for documented environment variables.
- Do not read `.env` unless the user explicitly asks for diagnostics and the task requires it.
- Validate and normalize all external input.
- Use parameterized SQL.
- Enforce authorization on the server side.
- Default to deny for privileged actions.
- Do not trust client-provided user IDs, roles, prices, paths, filenames, or ownership fields.
- Escape or encode untrusted output in UI and generated documents.
- Treat file uploads as untrusted:
  - validate type and size;
  - store outside committed paths;
  - avoid using original filenames as trusted paths.
- Avoid shelling out with user-controlled input.
- Do not log secrets, tokens, cookies, authorization headers, or sensitive PII.
- Prefer short-lived, least-privilege credentials when credentials are unavoidable.
- Evaluate new dependencies for maintenance, license, transitive risk, install-time scripts, telemetry, and binary downloads.

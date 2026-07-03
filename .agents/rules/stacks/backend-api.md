---
id: stack.backend-api
kind: stack
triggers:
  - 'backend API'
  - 'HTTP API'
  - 'handlers'
  - 'authorization'
  - 'pagination'
  - 'error shape'
summary: Apply backend API defaults for contracts, validation, authorization, and handlers.
load_with: []
---

# Backend API Rules

- Define API contracts before implementation when clients depend on them.
- Prefer stable error shapes.
- Validate request input at the boundary.
- Keep authorization server-side.
- Use idempotency for retryable mutation endpoints when practical.
- Use pagination for list endpoints that can grow.
- Avoid leaking internal errors to clients.
- Log internal errors with safe context.
- Keep handlers thin:
  - parse/validate;
  - authorize;
  - call use case/service;
  - map result/error to response.
- Keep domain rules out of transport handlers.

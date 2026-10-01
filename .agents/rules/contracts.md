# Contracts and Compatibility

## Identify consumers before imposing compatibility

For an API, config, CLI, schema, persisted format, or generated interface, inspect who consumes it, whether producer and consumer upgrade together, and what stability is promised.

- Internal details whose consumers can be updated together can change within the task's scope. Being exported, documented, or generated does not alone make an interface durable.
- External consumers, independently upgraded clients or automation, explicit stability promises, and data that must remain readable or writable create compatibility obligations.
- When consumers are unclear, inspect call sites, release boundaries, documentation, and persisted-data use. Ask only if an unresolved compatibility decision affects the requested result or needs authorization.

Preserve required compatibility unless the user authorizes the specific break. That authorization includes necessary consumer, documentation, and validation updates within its scope. For persisted data, establish which existing data must remain usable and validate the chosen transition. Migration and recovery methods follow the project and task; destructive real-data operations retain the root authorization and recovery requirements.

## New interfaces

- For a new JSON HTTP API, document methods, paths, authentication, bodies, errors, and relevant pagination or compatibility behavior in Markdown.
- Prefer the following error shape, with `details` only when the interface defines it:

```json
{ "error": { "code": "stable_code", "message": "Safe message", "details": {} } }
```

- Choose unknown-field handling for each interface. Rejecting unknown config keys can catch typos; extensible protocols and forward-compatible passthrough may need different behavior. Preserve an existing interface's policy.
- Define config precedence when config sources are needed; do not invent environment overrides without a use case.

## Time and normalization

- Prefer UTC RFC 3339 for instants in new JSON interfaces. Choose precision and internal representation to preserve required information; do not impose milliseconds everywhere.
- Distinguish calendar dates from instants, and stored values from localized presentation.
- Define case, whitespace, Unicode, filename, or path normalization and collision behavior at the relevant interface. Do not silently change an existing contract's normalization.

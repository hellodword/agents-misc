---
id: core.config-schema-protocol-api
kind: core
triggers:
  - 'config'
  - 'schema'
  - 'protocol'
  - 'API contract'
  - 'FFI contract'
---

# Config, Schema, Protocol, and API Rules

- Prefer designing contracts before implementation when multiple components depend on them.
- YAML preference applies only to project-developed application configuration files when multiple formats are equally valid.
- For tool-defined config files, use the tool's required or conventional format.
- Default API documentation format: Markdown contract first.

## Config contracts

Document:

- file path;
- format;
- version field when durable;
- field names;
- types;
- defaults;
- required/optional status;
- validation rules;
- environment variable overrides;
- precedence order;
- example config;
- migration behavior.

## HTTP API contracts

Document:

- method and path;
- request schema;
- response schema;
- status codes;
- auth requirements;
- idempotency;
- pagination/filtering/sorting;
- error shape;
- compatibility notes.

## Protocol/event contracts

Document:

- version;
- producer;
- consumer;
- ordering;
- retry;
- deduplication;
- compatibility rules;
- failure handling.

## FFI contracts

Document:

- ownership;
- lifetime;
- threading;
- cancellation;
- error mapping;
- serialization format;
- versioning;
- generated file expectations.

## Markdown contract first

Use Markdown first unless the API is already external, generated, or large enough to justify OpenAPI.

A Markdown API contract should be concise, example-heavy, and kept next to durable docs.

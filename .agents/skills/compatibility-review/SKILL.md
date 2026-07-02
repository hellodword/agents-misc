---
name: compatibility-review
description: Use this before changing APIs, config files, CLI flags, database schemas, persisted formats, generated contracts, or public behavior.
---

# Compatibility Review

## Purpose

Decide whether a change must preserve compatibility or may use explicit aggressive early-stage behavior.

## Workflow

1. Identify the contract being changed:
   - API;
   - config;
   - CLI;
   - database;
   - persisted file;
   - protocol;
   - generated contract.
2. Classify the contract:
   - experimental/internal;
   - durable/local;
   - public/external.
3. Check whether the user explicitly requested aggressive early-stage mode.
4. In durable mode, prefer additive changes and migrations.
5. In aggressive mode, allow breaking changes but document reset/data-loss behavior.
6. Update examples, tests, and docs.
7. Report compatibility impact and required migration/reset steps.

## Output

Provide:

- contract classification;
- compatibility mode;
- breaking changes;
- migration or reset plan;
- tests/docs updated;
- user decisions needed.

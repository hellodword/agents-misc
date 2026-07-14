---
name: compatibility-review
description: Use this before changing APIs, config files, CLI flags, database schemas, persisted formats, generated contracts, or public behavior.
---

# Compatibility Review

## Purpose

Classify the affected contract and select durable handling, one exact user-authorized exception, or a disclosed and confirmed aggressive scope.

## Workflow

1. Identify each API, config, CLI, database, persisted file, protocol, or generated contract being changed.
2. Classify it using `core.compatibility`:
   - experimental/internal only when it is unexported, undocumented, unpersisted, and has no durable consumer;
   - durable/local when it is persisted, documented, generated, or used by local users or automation;
   - public/external when external users or integrations consume it.
3. Treat unknown classification as durable/local.
4. Use durable compatibility unless either authorization path applies:
   - specific exception: record the exact contract, operation, impact, and authorizing user message;
   - aggressive mode: disclose all applicable bundled impacts and wait for the required later confirmation.
5. Apply authorization only to its exact scope. Never infer real-user-data loss or unrelated breaks.
6. Update examples, tests, docs, and migration or recovery instructions.

## Output

Provide:

- contract classification and evidence;
- compatibility mode;
- authorization scope/evidence when not durable;
- breaking changes;
- migration, deprecation, reset, or recovery plan;
- tests and docs updated;
- unresolved user decisions.

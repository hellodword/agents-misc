---
name: compatibility-review
description: Classify and review changes to APIs, config, CLI behavior, database schemas, persisted formats, generated contracts, and public behavior. Use before changing a durable or external contract; do not use for unexported, undocumented, unpersisted internal refactors with no durable consumer.
---

# Compatibility Review

1. Identify every API, config, CLI, database, persisted file, protocol, generated interface, or documented behavior affected.
2. Classify each contract as internal, durable/local, or public/external using the shared contracts rule. Treat unknown as durable/local.
3. Preserve durable and public behavior unless the user explicitly authorizes the exact breaking contract and operation.
4. Record the authorizing request, affected contract, impact, and exact scope. Do not infer authorization for data loss, unrelated breakage, deployment, or publication.
5. Choose the smallest compatible design when authorization does not cover a break.
6. When a break is authorized, remove or migrate only the named behavior and document how consumers recover or adapt.
7. Update examples, docs, contract tests, migrations, deprecation notes, and recovery instructions that belong to the affected interface.
8. Report classification evidence, authorization, breaking behavior, migration/recovery, tests, documentation, and unresolved decisions.

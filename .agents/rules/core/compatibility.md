---
id: core.compatibility
kind: core
triggers:
  - "compatibility"
  - "breaking change"
  - "deprecation"
  - "migration path"
  - "contract stage"
summary: Preserve durable contracts unless aggressive early-stage behavior is explicit.
load_with: []
---

# Compatibility Rules

## Default mode: durable compatibility

Use durable compatibility unless the user explicitly asks for aggressive early-stage changes.

Durable compatibility means:

- prefer additive changes;
- preserve existing config keys where practical;
- preserve CLI flags or provide deprecated aliases;
- preserve API response fields where practical;
- preserve database data through migrations;
- keep error shapes stable;
- document breaking changes when unavoidable.

## Contract stages

Classify a contract before changing it:

1. Experimental/internal:
   - not documented for users;
   - not persisted;
   - not consumed across process/network/module boundaries;
   - can change freely within the current task.

2. Durable/local:
   - documented;
   - persisted;
   - used by local users;
   - requires migration or compatibility notes.

3. Public/external:
   - consumed by external users or integrations;
   - requires explicit versioning, deprecation, migration path, and tests.

## Aggressive early-stage mode

Use only when the user explicitly says one of:

- aggressive mode;
- early-stage aggressive mode;
- 可以破坏兼容;
- 可以重置数据;
- 可以不保留历史包袱;
- 早期激进更新.

Allowed in aggressive mode:

- replacing schemas instead of migrating them;
- squashing migrations;
- renaming config keys without compatibility aliases;
- changing CLI flags;
- changing API shapes;
- deleting old compatibility branches;
- resetting local dev data and fixtures.

Still required:

- clear documentation of data loss or reset behavior;
- updated examples;
- updated tests;
- no silent loss of real user data;
- no secret exposure;
- no unrelated rewrite.

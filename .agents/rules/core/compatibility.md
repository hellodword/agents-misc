---
id: core.compatibility
kind: core
triggers:
  - "compatibility"
  - "breaking change"
  - "deprecation"
  - "migration path"
  - "contract stage"
  - "aggressive mode"
summary: Classify changed contracts and require durable handling or explicit, scoped authorization for breaks.
companions: {}
---

# Compatibility Rules

Classify every affected contract before changing it:

1. **Experimental/internal**: unexported, undocumented, unpersisted, and not consumed across a process, package-public, generated, automation, or other durable boundary.
2. **Durable/local**: persisted, documented, generated for another component, or relied on by local users, scripts, tests, or automation.
3. **Public/external**: exported or documented for external users, clients, plugins, or integrations.

If evidence does not establish experimental/internal status, classify the contract as durable/local.

## Durable mode

Durable mode is the default. Prefer additive changes, preserve documented API/config/CLI/error behavior, migrate persisted data, and provide deprecation or versioned migration paths for durable and public contracts.

## Specific exception

An explicit user instruction may authorize one named incompatibility without activating aggressive mode. Record the exact contract, operation, affected consumers or data, and the message that authorized it. Apply the exception only to that scope; preserve compatibility everywhere else.

A specific exception does not authorize unrelated breaks, real-data loss, migration squashing, fixture resets, or other bundled permissions. Update affected tests, examples, documentation, and migration or recovery instructions.

## Aggressive mode authorization

Aggressive mode bundles permission for multiple categories: breaking API/CLI/config behavior, schema replacement or migration squashing, and resetting proven-disposable development/test data. It is active only after this protocol completes:

1. The user explicitly requests the whole aggressive mode for the current task. Authorizing one break or generally accepting incompatibility does not activate it.
2. Before changing anything, disclose every applicable current-task impact:
   - breaking API, CLI, or config behavior;
   - schema replacement or migration squashing;
   - development/test reset or fixture replacement;
   - data loss.
3. Wait for a later user message that explicitly confirms aggressive mode after the disclosure. A confirmation embedded in the initial request does not replace this later message.
4. Apply it only to the disclosed scope. Repeat disclosure and confirmation when scope expands.

Aggressive mode never authorizes real user-data loss, secret exposure, unrelated rewrites, external side effects, or action outside the disclosed task. Within the confirmed scope, document every breaking behavior and development/test reset, and update examples, tests, and docs.

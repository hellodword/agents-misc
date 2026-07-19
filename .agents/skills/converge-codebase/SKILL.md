---
name: converge-codebase
description: Use `converge-codebase` to audit or execute a repository-wide or subsystem-wide convergence of internal implementation onto current supported behavior by removing obsolete indirection, internal legacy paths, redundant or implementation-shaped tests, and dependent debris. Trigger only when the user explicitly invokes `converge-codebase` by name, including `$converge-codebase` or `/converge-codebase`; never auto-trigger from cleanup, refactoring, dead-code, legacy-removal, test-pruning, migration, compatibility, or de-bloating requests alone. Exclude public or durable contracts, persisted formats and database migrations, and generated artifacts.
---

# Converge the Codebase

Reduce internal implementation to the clearest direct expression of current supported behavior. Apply all governing repository instructions and routed rules; this workflow does not override them or grant authority to break contracts, destroy data, or expand scope.

## Choose the Mode

- Remain read-only when the user requests an audit, assessment, recommendations, or plan.
- Execute when the user explicitly requests implementation or confirms an audit's candidates. Treat that request as confirmation only for in-scope internal cleanup.
- Otherwise perform the audit, report the candidates, and wait for confirmation before editing.

## Hold the Boundary

Consider handwritten source, tests, fixtures, examples, scripts, documentation, internal configuration, and unused dependencies. Treat configuration as internal only when it is unexported, undocumented, unpersisted, and has no durable consumer.

Read durable and public behavior as a protected constraint, not a cleanup target. Do not change:

- Public or durable APIs, CLIs, configuration, protocols, or documented behavior.
- Persisted formats, database schemas, migration ledgers, upgrade paths, or real data.
- Generated bindings, clients, schemas, snapshots, metadata, or source.

Treat an unknown boundary as protected. Preserve and report any candidate that crosses one of these boundaries. Do not absorb or restate the specialist workflows that own them; route a separately requested boundary change through the existing compatibility, database, or generated-artifact workflow.

When removing an unused dependency, change its authoritative manifest and use the project's declared dependency tool to update any active lockfile. Never edit lockfile content directly.

## Audit the Current Implementation

1. Read the governing instructions and inspect the working tree, requested scope, active entrypoints, official validation commands, and relevant architecture.
2. Establish a representative validation baseline when feasible and distinguish existing failures from later regressions.
3. Trace active callers, readers, writers, configuration use, and operational workflows. Use repository history only to explain why a candidate exists, not as proof that it remains supported.
4. Do not infer that an externally visible surface is unused merely because no in-repository caller exists.
5. Classify each candidate as Delete, Inline, Merge, Rewrite, Retain, or Defer.

Use these evidence thresholds:

- **Delete** only when the target has no distinct current behavior, protected consumer, safety value, or maintenance role.
- **Inline** or **Merge** when indirection or duplication has no durable independent meaning.
- **Rewrite** when current behavior is valuable but its historical structure is not.
- **Retain** when concrete current evidence justifies the structure.
- **Defer** when evidence is incomplete or the candidate touches a protected boundary.

Report the audit as a compact table with `target`, `action`, `evidence`, `protected behavior`, `validation`, and `status`. Use `ready`, `defer`, or `out-of-scope` as the status. Stop after this report unless execution is already authorized.

## Prune Tests by Behavior Evidence

Retain tests that uniquely protect current observable behavior, business rules, security, permissions, data integrity, required rejection behavior, or a plausible high-impact regression.

Delete, merge, or rewrite a test only after proving that it:

- Primarily locks a private helper, wrapper, internal call order, file layout, symbol name, source text, or mock interaction count without an independent behavior contract.
- Duplicates behavior already protected at a stronger or more stable boundary.
- Targets a behavior or implementation that the approved cleanup removes and that no current contract requires.

Do not remove a test merely because it is negative, narrow, or inconvenient. Do not replace deleted tests one-for-one; add the smallest behavior-level coverage only when an important current contract would otherwise be unprotected.

## Execute in Batches

For each approved coherent batch:

1. Remove, inline, merge, or directly rewrite the target.
2. Remove dependent imports, exports, internal configuration, unused dependencies, fixtures, examples, scripts, and documentation in the same batch.
3. Do not leave transitional stubs, archive directories, cleanup helpers, speculative extension points, or new compatibility layers.
4. Run the narrowest meaningful validation and fix cleanup regressions before expanding the batch.

After the batches pass, run all feasible official validation, inspect the complete diff, and sweep once more for dead references, stale internal paths, production code used only by removed tests, and debris exposed by deletion. Do not add permanent source scans, absence checks, forbidden-name lists, or architecture locks to prevent reintroduction.

## Finish

Finish only when approved candidates are resolved or explicitly deferred, current supported behavior remains intact, relevant validation passes or has a precisely attributed blocker, the residual sweep is clean, and the final diff is materially simpler without unrelated changes or metric gaming.

Report the categories deleted, inlined, merged, rewritten, retained, or deferred; the behavior protected; test-pruning rationale; validation results; protected boundaries encountered; and unresolved evidence. Do not claim checks that were not run.

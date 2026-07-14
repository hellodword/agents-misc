---
name: validation-selection
description: Use this when choosing the smallest useful validation for code changes, including unit, integration, contract, UI, browser, migration, generated artifact, and Go race validation.
---

# Validation Selection

## Workflow

1. Identify the changed behavior and every boundary it crosses.
2. Select the focused validation defined by `core.testing`: unit, integration, contract/example, migration, generator-plus-consumer, component/widget, or browser E2E.
3. Choose browser E2E only for browser-specific behavior, a primary browser user flow, or a regression that cannot be meaningfully covered below the browser boundary.
4. Run package-, file-, or target-level validation first.
5. Add full-repository validation only for build/CI wiring, a lockfile or dependency graph, a public/shared contract, migration infrastructure, generated infrastructure, or an explicit project requirement.
6. For affected Go packages, add `go test -race` when the code touches concurrency or shared mutable state listed in `core.testing`.
7. Never weaken tests to hide a failure. Re-run the narrowest failure and attribute it as introduced, pre-existing, or environment-caused.

## Output

Report changed boundaries, selected commands, why broader validation was or was not required, Go race decision, failures and attribution, and environment limitations.
